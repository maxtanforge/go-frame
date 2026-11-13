package protocols

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
)

func TestXMLCallbackVerifiesOriginalBytes(t *testing.T) {
	raw := `<xml><MsgType>text</MsgType><MsgId>9223372036854775807</MsgId><Content><![CDATA[hello & world]]></Content></xml>`
	called := false
	h, e := NewXMLCallback(func(_ context.Context, _ *http.Request, b []byte) error {
		if string(b) != raw {
			t.Fatal("bytes changed")
		}
		return nil
	}, func(_ context.Context, m Message) (string, error) {
		called = true
		if m.ID != "9223372036854775807" || m.Content != "hello & world" {
			t.Fatalf("%+v", m)
		}
		return "success", nil
	})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	r.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != 200 || w.Body.String() != "success" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestXMLCallbackVerificationFailure(t *testing.T) {
	h, e := NewXMLCallback(func(context.Context, *http.Request, []byte) error {
		return &httperror.Error{Status: 401, Message: "验签失败"}
	}, func(context.Context, Message) (string, error) {
		t.Fatal("called after verification failure")
		return "", nil
	})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<xml><MsgType>text</MsgType></xml>`))
	r.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
