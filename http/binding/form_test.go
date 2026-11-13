package binding

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
)

func TestFormBinding(t *testing.T) {
	type Input struct {
		Name    string `v:"required" label:"名称"`
		Current int    `d:"1" v:"gte=1"`
	}
	h, e := Form(func(_ context.Context, in *Input) (*Input, error) { return in, nil })
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader("name=test"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Current":1`) {
		t.Fatal(w.Body.String())
	}
}
func TestMultipartBinding(t *testing.T) {
	type Input struct {
		Name string `v:"required" label:"名称"`
	}
	called := false
	d, e := PrepareMultipart[Input](MultipartOptions{Upload: request.MultipartOptions{FileFields: []string{"file"}}})
	if e != nil {
		t.Fatal(e)
	}
	h := respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		in, e := d.Decode(w, r, func(_ context.Context, u request.Upload) error { _, e := io.Copy(io.Discard, u.Reader); return e })
		if e != nil {
			return nil, e
		}
		called = true
		return in, nil
	})
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	f, _ := m.CreateFormFile("file", "x.txt")
	_, _ = io.WriteString(f, "data")
	_ = m.Close()
	r := httptest.NewRequest("POST", "/", &b)
	r.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || called || !strings.Contains(w.Body.String(), "名称") {
		t.Fatalf("%d %v %s", w.Code, called, w.Body.String())
	}
}
