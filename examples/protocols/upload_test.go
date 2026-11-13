package protocols

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"testing"
)

type panicUploadReader struct{ prefix []byte }

func (r *panicUploadReader) Read(p []byte) (int, error) {
	if len(r.prefix) == 0 {
		panic("upload reader failed")
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

func TestUploadClosesAndRemovesStagingAfterReaderPanic(t *testing.T) {
	dir := t.TempDir()
	h, err := NewUploadHandler(dir, func(context.Context, UploadInput, []StagedFile) (any, error) {
		t.Fatal("save called for incomplete upload")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Headers are delivered first so the callback opens the staging file. The
	// following read panics while io.Copy is using that open file.
	body := &panicUploadReader{prefix: []byte("--test-boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"input.txt\"\r\nContent-Type: application/octet-stream\r\n\r\npayload")}
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary=test-boundary")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("expected recovered panic, got %d", w.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging remained after panic: %v %v", entries, err)
	}
}

func TestUploadCleanupAndValidation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		dir := t.TempDir()
		called := false
		h, e := NewUploadHandler(dir, func(_ context.Context, in UploadInput, files []StagedFile) (any, error) {
			called = true
			if in.Title != "report" || len(files) != 1 {
				t.Fatalf("%+v %v", in, files)
			}
			raw, e := os.ReadFile(files[0].Path)
			if e != nil || string(raw) != "data" {
				t.Fatalf("%q %v", raw, e)
			}
			return struct{ Saved bool }{true}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		f, _ := m.CreateFormFile("file", "../../unsafe.txt")
		_, _ = io.WriteString(f, "data")
		if valid {
			_ = m.WriteField("title", "report")
		}
		_ = m.Close()
		r := httptest.NewRequest("POST", "/", &b)
		r.Header.Set("Content-Type", m.FormDataContentType())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if called != valid || valid && w.Code != 200 || !valid && w.Code != 400 {
			t.Fatalf("%d %v %s", w.Code, called, w.Body.String())
		}
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 0 {
			t.Fatalf("leftover files: %v %v", entries, e)
		}
	}
}
