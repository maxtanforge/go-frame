package request

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"unicode/utf8"
)

func FuzzJSONPreflight(f *testing.F) {
	for _, seed := range []string{`{"name":"ok"}`, `[1,2]`, `{"a":1,"a":2}`, `null`, `{} {}`, `[[[[[[[[[0]]]]]]]]]`, "\xff"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 4096 {
			return
		}
		r := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		value, err := DecodeJSON[any](httptest.NewRecorder(), r, DecodeOptions{MaxBytes: 2048, MaxDepth: 8, MaxFields: 8, MaxItems: 8})
		if err != nil {
			return
		}
		if len(raw) > 2048 || !utf8.Valid(raw) || !json.Valid(raw) || value == nil {
			t.Fatal("accepted invalid or oversized JSON")
		}
		var check func(any, int)
		check = func(v any, depth int) {
			switch node := v.(type) {
			case map[string]any:
				if depth >= 8 || len(node) > 8 {
					t.Fatal("object limit bypassed")
				}
				for _, child := range node {
					check(child, depth+1)
				}
			case []any:
				if depth >= 8 || len(node) > 8 {
					t.Fatal("array limit bypassed")
				}
				for _, child := range node {
					check(child, depth+1)
				}
			}
		}
		check(value, 0)
	})
}

func FuzzXMLPreflight(f *testing.F) {
	for _, seed := range []string{`<root/>`, `<root><item>ok</item></root>`, `<!DOCTYPE r><r/>`, `<a/><b/>`, `<a>`, `text<a/>`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 4096 {
			return
		}
		o := XMLOptions{MaxBytes: 2048, MaxDepth: 8, MaxNodes: 16}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(checkXML(ctx, raw, o), context.Canceled) {
			t.Fatal("ignored cancellation")
		}
		if err := checkXML(context.Background(), raw, o); err != nil {
			return
		}
		if len(raw) > 2048 {
			t.Fatal("byte limit bypassed")
		}
		d := xml.NewDecoder(bytes.NewReader(raw))
		depth, roots, nodes := 0, 0, 0
		for {
			token, err := d.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal("accepted malformed XML", err)
			}
			switch token := token.(type) {
			case xml.StartElement:
				if depth == 0 {
					roots++
				}
				depth++
				nodes++
				if depth > 8 || nodes > 16 {
					t.Fatal("structure limit bypassed")
				}
			case xml.EndElement:
				depth--
			case xml.Directive:
				t.Fatal("accepted directive")
			case xml.CharData:
				if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
					t.Fatal("accepted text outside root")
				}
			}
		}
		if roots != 1 || depth != 0 {
			t.Fatal("accepted multiple or incomplete roots")
		}
	})
}

func FuzzStreamMultipart(f *testing.F) {
	f.Add([]byte("--test\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\nabc\r\n--test--\r\n"), "test")
	f.Add([]byte("--test\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nvalue\r\n--test--\r\n"), "test")
	f.Add([]byte("--test\r\n"), "test")
	f.Fuzz(func(t *testing.T, raw []byte, boundary string) {
		if len(raw) > 8192 || len(boundary) > 128 {
			return
		}
		body := bytes.NewReader(raw)
		r := httptest.NewRequest("POST", "/", body)
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		files := 0
		values, err := StreamMultipart(httptest.NewRecorder(), r, func(_ context.Context, upload Upload) error {
			files++
			if files > 2 || upload.Field != "file" {
				t.Fatal("file policy bypassed")
			}
			n, readErr := io.Copy(io.Discard, upload.Reader)
			if n > 257 {
				t.Fatal("file byte limit bypassed")
			}
			return readErr
		}, MultipartOptions{MaxBytes: 2048, MaxFileBytes: 256, MaxFieldBytes: 64, MaxFiles: 2, MaxFields: 4, MaxParts: 6, FileFields: []string{"file"}})
		if len(raw)-body.Len() > 2049 {
			t.Fatal("body read exceeded limit plus probe byte")
		}
		if r.MultipartForm != nil {
			t.Fatal("streaming parser created a buffered form")
		}
		if err != nil {
			return
		}
		fields := 0
		for _, items := range values {
			for _, item := range items {
				fields++
				if len(item) > 64 {
					t.Fatal("field byte limit bypassed")
				}
			}
		}
		if fields > 4 || fields+files > 6 {
			t.Fatal("part count bypassed")
		}
	})
}
