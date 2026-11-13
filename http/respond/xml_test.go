package respond

import (
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestXMLAndTextDirect(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v := struct {
		XMLName xml.Name `xml:"reply"`
		Text    string   `xml:",cdata"`
	}{Text: "a < b"}
	if err := XML(w, r, 201, v); err != nil {
		t.Fatal(err)
	}
	if w.Code != 201 || w.Body.String() != `<reply><![CDATA[a < b]]></reply>` || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("%+v", w)
	}
	w = httptest.NewRecorder()
	if err := Text(w, r, 200, "success"); err != nil || w.Body.String() != "success" {
		t.Fatalf("%s %v", w.Body.String(), err)
	}
}
func TestXMLMarshalBeforeHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	err := XML(w, httptest.NewRequest("GET", "/", nil), 201, make(chan int))
	if err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Fatalf("%v %+v", err, w)
	}
}
