package request

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/maxtanforge/go-frame/http/httperror"
)

// XMLOptions bounds XML input. Zero selects 1 MiB, 32 levels and 10000
// element nodes (including the root). Negative limits are schema errors.
type XMLOptions struct {
	MaxBytes           int64
	MaxDepth, MaxNodes int
}

func xmlOptions(options []XMLOptions) (XMLOptions, error) {
	o := XMLOptions{}
	if len(options) > 1 {
		return o, ErrSchema
	}
	if len(options) == 1 {
		o = options[0]
	}
	if o.MaxBytes < 0 || o.MaxDepth < 0 || o.MaxNodes < 0 {
		return o, ErrSchema
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 1 << 20
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = 32
	}
	if o.MaxNodes == 0 {
		o.MaxNodes = 10000
	}
	return o, nil
}

type xmlContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r xmlContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if e := r.ctx.Err(); e != nil {
		return n, e
	}
	return n, err
}
func xmlInvalid(err error) error {
	return &httperror.Error{Status: 400, Message: "Invalid XML body", Err: err}
}
func xmlLimit(err error) error {
	return &httperror.Error{Status: 413, Message: "XML input limit exceeded", Err: err}
}

// ReadXML returns the exact original bytes after checking media type, size and
// document structure. The caller may verify a signature before DecodeXMLBytes.
// It does not close the server-owned body. Context checks cannot interrupt an
// arbitrary blocked Reader; configure server read timeouts as well.
func ReadXML(w http.ResponseWriter, r *http.Request, options ...XMLOptions) ([]byte, error) {
	o, err := xmlOptions(options)
	if err != nil {
		return nil, err
	}
	if err = r.Context().Err(); err != nil {
		return nil, err
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/xml" && media != "text/xml" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+xml"))) {
		return nil, &httperror.Error{Status: 415, Message: "Content-Type must be XML"}
	}
	if r.Body == nil {
		return nil, xmlInvalid(io.EOF)
	}
	raw, err := io.ReadAll(xmlContextReader{r.Context(), http.MaxBytesReader(w, r.Body, o.MaxBytes)})
	if err != nil {
		if e := r.Context().Err(); e != nil {
			return nil, e
		}
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return nil, xmlLimit(err)
		}
		return nil, xmlInvalid(err)
	}
	if err = checkXML(r.Context(), raw, o); err != nil {
		return nil, err
	}
	return raw, nil
}

func checkXML(ctx context.Context, raw []byte, o XMLOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(raw)) > o.MaxBytes {
		return xmlLimit(errors.New("byte limit"))
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	depth, nodes, roots := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return xmlInvalid(err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return xmlInvalid(errors.New("multiple roots"))
				}
			}
			depth++
			nodes++
			if depth > o.MaxDepth || nodes > o.MaxNodes {
				return xmlLimit(errors.New("structure limit"))
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return xmlInvalid(errors.New("XML directives are not supported"))
		case xml.CharData:
			if depth == 0 && len(bytes.Trim(t, " \t\r\n")) > 0 {
				return xmlInvalid(errors.New("text outside root"))
			}
		}
	}
	if roots != 1 || depth != 0 {
		return xmlInvalid(errors.New("expected one XML root"))
	}
	return ctx.Err()
}

// DecodeXML decodes a bounded document into a struct using encoding/xml tags.
// Unknown elements follow encoding/xml semantics and are ignored. Validation,
// defaults and presence tracking are not performed by this low-level API.
func DecodeXML[T any](w http.ResponseWriter, r *http.Request, options ...XMLOptions) (T, error) {
	var zero T
	if err := CheckXMLSchema(reflect.TypeFor[T]()); err != nil {
		return zero, err
	}
	raw, err := ReadXML(w, r, options...)
	if err != nil {
		return zero, err
	}
	return decodeCheckedXML[T](r.Context(), raw)
}

// DecodeXMLBytes validates bounded raw XML and applies native XML mapping.
// Custom UnmarshalXML methods run synchronously and must cooperate with their
// own cancellation needs. The raw slice must not be mutated concurrently.
func DecodeXMLBytes[T any](ctx context.Context, raw []byte, options ...XMLOptions) (T, error) {
	var value T
	if err := CheckXMLSchema(reflect.TypeFor[T]()); err != nil {
		return value, err
	}
	o, err := xmlOptions(options)
	if err != nil {
		return value, err
	}
	if err = checkXML(ctx, raw, o); err != nil {
		return value, err
	}
	return decodeCheckedXML[T](ctx, raw)
}

func decodeCheckedXML[T any](ctx context.Context, raw []byte) (T, error) {
	var value T
	err := xml.NewDecoder(xmlContextReader{ctx, bytes.NewReader(raw)}).Decode(&value)
	if e := ctx.Err(); e != nil {
		return value, e
	}
	if err != nil {
		var syntax *xml.SyntaxError
		var mismatch xml.UnmarshalError
		var number *strconv.NumError
		if errors.As(err, &syntax) || errors.As(err, &mismatch) || errors.As(err, &number) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return value, xmlInvalid(err)
		}
		return value, err
	}
	return value, nil
}
