package binding

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/validation"
)

type xmlDTO struct {
	XMLName xml.Name `xml:"input" json:"-"`
	Name    string   `xml:"name" v:"required" label:"名称"`
	Email   string   `xml:"email" v:"required,email" label:"邮箱"`
}

func TestXMLDiagnosticsAndSafeHTTP(t *testing.T) {
	d, err := PrepareXML[xmlDTO]()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<input/>`))
	r.Header.Set("Content-Type", "text/xml")
	_, err = d.Decode(httptest.NewRecorder(), r)
	var all *validation.Errors
	if !errors.As(err, &all) || len(all.Issues()) < 2 {
		t.Fatalf("%v", err)
	}
	h, err := XML(func(_ context.Context, in *xmlDTO) (*xmlDTO, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`<input/>`))
	r.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), all.FirstMessage()) || strings.Contains(w.Body.String(), `"issues"`) {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestXMLUnsupportedPresenceAndDefaults(t *testing.T) {
	if _, err := PrepareXML[struct {
		Name string `xml:"name" d:"guest"`
	}](); !errors.Is(err, validation.ErrSchema) {
		t.Fatal(err)
	}
	if _, err := PrepareXML[struct {
		Name string `xml:"name" v:"present"`
	}](); !errors.Is(err, validation.ErrSchema) {
		t.Fatal(err)
	}
}

func TestXMLPreparationRejectsNativeDeclarations(t *testing.T) {
	checks := []struct {
		name    string
		prepare func() error
	}{
		{"duplicate", func() error {
			_, err := PrepareXML[struct {
				A string `xml:"B"`
				B string
			}]()
			return err
		}},
		{"path", func() error {
			_, err := PrepareXML[struct {
				A string `xml:"a"`
				B string `xml:"a>b"`
			}]()
			return err
		}},
		{"mode", func() error {
			_, err := PrepareXML[struct {
				A string `xml:"a,attr,chardata"` //nolint:staticcheck // Deliberately conflicting modes test registration rejection.
			}]()
			return err
		}},
		{"nested", func() error {
			_, err := PrepareXML[struct {
				N struct {
					A string `xml:"B"`
					B string
				}
			}]()
			return err
		}},
		{"type", func() error {
			_, err := PrepareXML[struct {
				A map[string]string `xml:"a"`
			}]()
			return err
		}},
		{"chardata-type", func() error {
			_, err := PrepareXML[struct {
				A []int `xml:",chardata"`
			}]()
			return err
		}},
		{"element-multiple-pointers", func() error {
			_, err := PrepareXML[struct {
				A **string `xml:"a"`
			}]()
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.prepare(); !errors.Is(err, validation.ErrSchema) {
				t.Fatalf("expected schema error, got %v", err)
			}
		})
	}
}

func TestXMLPointerToCustomDecoder(t *testing.T) {
	xmlCustomCalls = 0
	d, err := PrepareXML[struct {
		A *XMLCustom `xml:"a"`
	}]()
	if err != nil || xmlCustomCalls != 0 {
		t.Fatalf("preflight: calls=%d err=%v", xmlCustomCalls, err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<root><a>custom</a></root>`))
	r.Header.Set("Content-Type", "text/xml")
	v, err := d.Decode(httptest.NewRecorder(), r)
	if err != nil || v.A == nil || v.A.Value != "custom" || xmlCustomCalls != 1 {
		t.Fatalf("decode: %+v calls=%d err=%v", v, xmlCustomCalls, err)
	}
}

var xmlCustomCalls int

type XMLCustom struct{ Value string }

func (v *XMLCustom) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	xmlCustomCalls++
	return d.DecodeElement(&v.Value, &start)
}
func (*XMLCustom) MarshalXML(*xml.Encoder, xml.StartElement) error {
	panic("preflight called MarshalXML")
}

type xmlCustomText string

func (*xmlCustomText) UnmarshalText([]byte) error  { panic("preflight called UnmarshalText") }
func (xmlCustomText) MarshalText() ([]byte, error) { panic("preflight called MarshalText") }

func TestXMLPreparationDoesNotExecuteBusinessMethods(t *testing.T) {
	xmlCustomCalls = 0
	if _, err := PrepareXML[struct {
		Text   xmlCustomText `xml:",chardata"`
		Custom XMLCustom     `xml:"custom"`
	}](); err != nil {
		t.Fatal(err)
	}
	d, err := PrepareXML[XMLCustom]()
	if err != nil {
		t.Fatal(err)
	}
	if xmlCustomCalls != 0 {
		t.Fatalf("preflight executed custom decoder %d times", xmlCustomCalls)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<custom>works</custom>`))
	r.Header.Set("Content-Type", "text/xml")
	v, err := d.Decode(httptest.NewRecorder(), r)
	if err != nil || v.Value != "works" {
		t.Fatalf("%+v %v", v, err)
	}
	if xmlCustomCalls != 1 {
		t.Fatalf("decode called custom decoder %d times", xmlCustomCalls)
	}
}

type XMLPromotedCustom struct{ XMLCustom }

func TestXMLPreparationWithPromotedCustomDecoder(t *testing.T) {
	xmlCustomCalls = 0
	d, err := PrepareXML[XMLPromotedCustom]()
	if err != nil || xmlCustomCalls != 0 {
		t.Fatalf("preflight: calls=%d err=%v", xmlCustomCalls, err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<custom>promoted</custom>`))
	r.Header.Set("Content-Type", "text/xml")
	v, err := d.Decode(httptest.NewRecorder(), r)
	if err != nil || v.Value != "promoted" || xmlCustomCalls != 1 {
		t.Fatalf("decode: %+v calls=%d err=%v", v, xmlCustomCalls, err)
	}
}

type XMLEmbeddedFields struct {
	Name string `xml:"name"`
}

func TestXMLPreparationPreservesNativeEmbedding(t *testing.T) {
	d, err := PrepareXML[struct {
		XMLEmbeddedFields
		Name string `xml:"name"`
	}]()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<root><name>outer</name></root>`))
	r.Header.Set("Content-Type", "text/xml")
	v, err := d.Decode(httptest.NewRecorder(), r)
	if err != nil || v.Name != "outer" || v.XMLEmbeddedFields.Name != "" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestXMLSuccessUnwrapped(t *testing.T) {
	h, err := XML(func(_ context.Context, in *xmlDTO) (*xmlDTO, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<input><name>Ada</name><email>ada@example.com</email></input>`))
	r.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "<input>") || strings.Contains(w.Body.String(), `"success"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
