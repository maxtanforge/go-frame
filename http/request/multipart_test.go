package request

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
)

func uploadRequest(t *testing.T, content string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	if err := m.WriteField("name", "test"); err != nil {
		t.Fatal(err)
	}
	f, err := m.CreateFormFile("file", "input.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, content); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, m.FormDataContentType()
}

func TestMultipartWholeBodyLimitWithReadingCallback(t *testing.T) {
	b, media := uploadRequest(t, strings.Repeat("x", 8192))
	r := httptest.NewRequest("POST", "/", b)
	r.Header.Set("Content-Type", media)
	_, err := StreamMultipart(httptest.NewRecorder(), r, func(_ context.Context, u Upload) error { _, e := io.Copy(io.Discard, u.Reader); return e }, MultipartOptions{MaxBytes: 1024, FileFields: []string{"file"}})
	var h *httperror.Error
	if !errors.As(err, &h) || h.Status != 413 {
		t.Fatalf("expected 413, got %v", err)
	}
}

func TestMultipartBoundsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		o      MultipartOptions
		status int
	}{
		{"field", MultipartOptions{FileFields: []string{"file"}, MaxFieldBytes: 2}, 413},
		{"parts", MultipartOptions{FileFields: []string{"file"}, MaxParts: 1}, 413},
		{"type", MultipartOptions{FileFields: []string{"file"}, AllowedMediaTypes: []string{"image/png"}}, 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, media := uploadRequest(t, "hello")
			r := httptest.NewRequest("POST", "/", b)
			r.Header.Set("Content-Type", media)
			_, err := StreamMultipart(httptest.NewRecorder(), r, func(context.Context, Upload) error { return nil }, tc.o)
			var h *httperror.Error
			if !errors.As(err, &h) || h.Status != tc.status {
				t.Fatal(err)
			}
		})
	}
	b, media := uploadRequest(t, "hello")
	r := httptest.NewRequest("POST", "/", b)
	r.Header.Set("Content-Type", media)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	_, err := StreamMultipart(httptest.NewRecorder(), r, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMultipartTrailingBodyLimit(t *testing.T) {
	b, media := uploadRequest(t, "hello")
	limit := int64(b.Len() + 4)
	b.WriteString(strings.Repeat("x", 8192))
	r := httptest.NewRequest("POST", "/", b)
	r.Header.Set("Content-Type", media)
	_, err := StreamMultipart(httptest.NewRecorder(), r, func(context.Context, Upload) error { return nil }, MultipartOptions{MaxBytes: limit, FileFields: []string{"file"}})
	var h *httperror.Error
	if !errors.As(err, &h) || h.Status != 413 {
		t.Fatal(err)
	}
}
func TestStreamMultipart(t *testing.T) {
	b, media := uploadRequest(t, "hello")
	r := httptest.NewRequest("POST", "/?name=wrong", b)
	r.Header.Set("Content-Type", media)
	var got string
	fields, err := StreamMultipart(httptest.NewRecorder(), r, func(_ context.Context, u Upload) error {
		if u.Field != "file" || u.Filename != "input.txt" {
			t.Fatalf("%+v", u)
		}
		raw, e := io.ReadAll(u.Reader)
		got = string(raw)
		return e
	}, MultipartOptions{FileFields: []string{"file"}})
	if err != nil || got != "hello" || fields.Get("name") != "test" {
		t.Fatalf("%v %q %v", fields, got, err)
	}
}
func TestMultipartLimitsEvenUnreadFile(t *testing.T) {
	for _, read := range []bool{false, true} {
		b, media := uploadRequest(t, "toolong")
		r := httptest.NewRequest("POST", "/", b)
		r.Header.Set("Content-Type", media)
		_, err := StreamMultipart(httptest.NewRecorder(), r, func(_ context.Context, u Upload) error {
			if read {
				_, e := io.Copy(io.Discard, u.Reader)
				return e
			}
			return nil
		}, MultipartOptions{FileFields: []string{"file"}, MaxFileBytes: 3})
		var h *httperror.Error
		if !errors.As(err, &h) || h.Status != 413 {
			t.Fatalf("%v", err)
		}
	}
}
func TestMultipartCallbackError(t *testing.T) {
	b, media := uploadRequest(t, "hello")
	r := httptest.NewRequest("POST", "/", b)
	r.Header.Set("Content-Type", media)
	want := errors.New("storage unavailable")
	_, err := StreamMultipart(httptest.NewRecorder(), r, func(context.Context, Upload) error { return want }, MultipartOptions{FileFields: []string{"file"}})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestMultipartTruncatedFileIsClientError(t *testing.T) {
	for _, swallow := range []bool{false, true} {
		b, media := uploadRequest(t, "hello")
		raw := b.Bytes()
		raw = raw[:len(raw)-20]
		r := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
		r.Header.Set("Content-Type", media)
		_, err := StreamMultipart(httptest.NewRecorder(), r, func(_ context.Context, u Upload) error {
			_, e := io.Copy(io.Discard, u.Reader)
			if swallow {
				return nil
			}
			return e
		}, MultipartOptions{FileFields: []string{"file"}})
		var h *httperror.Error
		if !errors.As(err, &h) || h.Status != 400 {
			t.Fatalf("swallow=%v got %v", swallow, err)
		}
	}
}
func TestMultipartRejectsUnregisteredFile(t *testing.T) {
	b, media := uploadRequest(t, "hello")
	r := httptest.NewRequest("POST", "/", b)
	r.Header.Set("Content-Type", media)
	called := false
	_, err := StreamMultipart(httptest.NewRecorder(), r, func(context.Context, Upload) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
