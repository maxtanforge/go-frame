package request

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// MultipartOptions bounds the complete request and each streamed part. Files
// require explicit field names. AllowedMediaTypes checks declarations only;
// inspect file contents in the callback when a verified format is required.
type MultipartOptions struct {
	MaxBytes, MaxFileBytes, MaxFieldBytes int64
	MaxFiles, MaxFields, MaxParts         int
	FileFields, AllowedMediaTypes         []string
}

// Upload is one streamed file handed to the callback.
// Reader is valid only during the synchronous callback. Filename is
// untrusted metadata, never a storage path. Do not retain Reader or use it in
// another goroutine. Store temporarily until the complete request is validated.
type Upload struct {
	Field, Filename, MediaType string
	Reader                     io.Reader
}

// UploadHandler stages one uploaded file; return an error to abort the stream.
type UploadHandler func(context.Context, Upload) error

func (o MultipartOptions) normalized() (MultipartOptions, error) {
	if o.MaxBytes < 0 || o.MaxFileBytes < 0 || o.MaxFieldBytes < 0 || o.MaxFiles < 0 || o.MaxFields < 0 || o.MaxParts < 0 || o.MaxFileBytes == math.MaxInt64 || o.MaxFieldBytes == math.MaxInt64 {
		return o, ErrSchema
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 8 << 20
	}
	if o.MaxFileBytes == 0 {
		o.MaxFileBytes = 4 << 20
	}
	if o.MaxFieldBytes == 0 {
		o.MaxFieldBytes = 4096
	}
	if o.MaxFiles == 0 {
		o.MaxFiles = 4
	}
	if o.MaxFields == 0 {
		o.MaxFields = 64
	}
	if o.MaxParts == 0 {
		o.MaxParts = 128
	}
	seen := map[string]bool{}
	for _, name := range o.FileFields {
		if !namePattern.MatchString(name) || seen[name] {
			return o, ErrSchema
		}
		seen[name] = true
	}
	for _, value := range o.AllowedMediaTypes {
		media, params, e := mime.ParseMediaType(value)
		if e != nil || len(params) > 0 || media != value || strings.Contains(value, "*") {
			return o, ErrSchema
		}
	}
	return o, nil
}

// Validate checks options before accepting requests. Zero limits use defaults.
func (o MultipartOptions) Validate() error { _, e := o.normalized(); return e }

type uploadReader struct {
	ctx context.Context
	src io.Reader
	err error
}

func (r *uploadReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		r.err = e
		return 0, e
	}
	n, e := r.src.Read(p)
	if e != nil && e != io.EOF {
		r.err = e
	}
	return n, e
}

// StreamMultipart streams files to onFile and returns bounded form fields.
// It creates no temporary files. Callbacks may have run before a later request
// error: the caller owns staging, rollback and final commit. On error the unread
// request is not drained; net/http owns closing the incoming body.
func StreamMultipart(w http.ResponseWriter, r *http.Request, onFile UploadHandler, options ...MultipartOptions) (url.Values, error) {
	if len(options) > 1 {
		return nil, ErrSchema
	}
	o := MultipartOptions{}
	if len(options) == 1 {
		o = options[0]
	}
	o, err := o.normalized()
	if err != nil {
		return nil, err
	}
	if err = r.Context().Err(); err != nil {
		return nil, err
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		return nil, inputFailure(415)
	}
	if r.Body == nil {
		return nil, inputFailure(400)
	}
	// Use a shallow request copy; never populate ParseForm/MultipartForm caches.
	copyRequest := *r
	copyRequest.Body = http.MaxBytesReader(w, r.Body, o.MaxBytes)
	m, err := copyRequest.MultipartReader()
	if err != nil {
		return nil, inputFailure(400)
	}
	allowed := map[string]bool{}
	for _, name := range o.FileFields {
		allowed[name] = true
	}
	types := map[string]bool{}
	for _, name := range o.AllowedMediaTypes {
		types[name] = true
	}
	values := url.Values{}
	parts, files, fields := 0, 0, 0
	for {
		if err = r.Context().Err(); err != nil {
			return nil, err
		}
		// RawPart preserves the transmitted bytes: no implicit transfer decoding.
		part, e := m.NextRawPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, bodyFailure(e)
		}
		parts++
		if parts > o.MaxParts {
			return nil, inputFailure(413)
		}
		disposition, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := params["name"]
		if e != nil || disposition != "form-data" || name == "" || len(name) > 512 || part.Header.Get("Content-Transfer-Encoding") != "" {
			return nil, inputFailure(400)
		}
		_, isFile := params["filename"]
		if !isFile {
			fields++
			if fields > o.MaxFields {
				return nil, inputFailure(413)
			}
			raw, e := io.ReadAll(io.LimitReader(&uploadReader{ctx: r.Context(), src: part}, o.MaxFieldBytes+1))
			if e != nil {
				return nil, bodyFailure(e)
			}
			if int64(len(raw)) > o.MaxFieldBytes {
				return nil, inputFailure(413)
			}
			values.Add(name, string(raw))
		} else {
			files++
			if files > o.MaxFiles {
				return nil, inputFailure(413)
			}
			if !allowed[name] || onFile == nil {
				return nil, inputFailure(400)
			}
			contentType := part.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			media, _, e := mime.ParseMediaType(contentType)
			if e != nil {
				return nil, inputFailure(400)
			}
			if len(types) > 0 && !types[media] {
				return nil, inputFailure(415)
			}
			stream := &uploadReader{ctx: r.Context(), src: part}
			limited := &io.LimitedReader{R: stream, N: o.MaxFileBytes + 1}
			e = onFile(r.Context(), Upload{Field: name, Filename: part.FileName(), MediaType: media, Reader: limited})
			if stream.err != nil {
				return nil, bodyFailure(stream.err)
			}
			if limited.N == 0 {
				return nil, inputFailure(413)
			}
			if e != nil {
				if r.Context().Err() != nil {
					return nil, r.Context().Err()
				}
				return nil, fmt.Errorf("multipart upload: %w", e)
			}
			// A callback may inspect only a prefix; still enforce the full part limit.
			_, e = io.Copy(io.Discard, limited)
			if e != nil {
				return nil, bodyFailure(e)
			}
			if limited.N == 0 {
				return nil, inputFailure(413)
			}
		}
		if e = part.Close(); e != nil {
			return nil, bodyFailure(e)
		}
	}
	// The epilogue is part of the total body limit even after the final boundary.
	if _, err = io.Copy(io.Discard, copyRequest.Body); err != nil {
		return nil, bodyFailure(err)
	}
	if err = r.Context().Err(); err != nil {
		return nil, err
	}
	return values, nil
}
