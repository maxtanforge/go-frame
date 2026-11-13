// Package protocols demonstrates protocol adapters with business-owned effects.
package protocols

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
)

// UploadInput carries the validated form fields beside the streamed file.
type UploadInput struct {
	Title string `v:"required,max=100" label:"标题"`
}

// StagedFile records where business code persisted one uploaded file.
type StagedFile struct{ Path, Filename, MediaType string }

// NewUploadHandler owns per-request staging in a caller-provisioned private
// directory. Save must consume/copy the files synchronously; paths are removed
// before returning. Authentication/authorization are mounted by the application.
func NewUploadHandler(tempDir string, save func(context.Context, UploadInput, []StagedFile) (any, error)) (http.Handler, error) {
	if tempDir == "" || save == nil {
		return nil, errors.New("upload requires staging directory and save callback")
	}
	decoder, err := binding.PrepareMultipart[UploadInput](binding.MultipartOptions{Upload: request.MultipartOptions{FileFields: []string{"file"}, MaxFiles: 1}})
	if err != nil {
		return nil, err
	}
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (result any, err error) {
		var staged []StagedFile
		defer func() {
			for _, file := range staged {
				if e := os.Remove(file.Path); e != nil && !errors.Is(e, os.ErrNotExist) {
					err = errors.Join(err, e)
				}
			}
		}()
		input, err := decoder.Decode(w, r, func(_ context.Context, u request.Upload) (copyErr error) {
			file, e := os.CreateTemp(tempDir, "upload-*")
			if e != nil {
				return e
			}
			// Close before the outer staging cleanup, including panic unwinding.
			defer func() { copyErr = errors.Join(copyErr, file.Close()) }()
			staged = append(staged, StagedFile{Path: file.Name(), Filename: u.Filename, MediaType: u.MediaType})
			_, copyErr = io.Copy(file, u.Reader)
			return copyErr
		})
		if err != nil {
			return nil, err
		}
		if len(staged) != 1 {
			return nil, &httperror.Error{Status: 400, Message: "请选择文件"}
		}
		return save(r.Context(), input, staged)
	}), nil
}
