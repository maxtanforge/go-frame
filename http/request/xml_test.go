package request

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
)

type xmlInput struct {
	XMLName xml.Name `xml:"message"`
	ID      string   `xml:"id,attr"`
	Text    string   `xml:"text"`
	Nested  struct {
		Value int `xml:"value"`
	} `xml:"nested"`
}

func TestXMLNativeAndRaw(t *testing.T) {
	raw := `<message id="a"><text><![CDATA[a < b]]></text><nested><value>7</value></nested></message>`
	r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/vendor+xml")
	got, err := ReadXML(httptest.NewRecorder(), r)
	if err != nil || string(got) != raw {
		t.Fatalf("raw: %q %v", got, err)
	}
	v, err := DecodeXMLBytes[xmlInput](r.Context(), got)
	if err != nil || v.ID != "a" || v.Text != "a < b" || v.Nested.Value != 7 {
		t.Fatalf("decode: %+v %v", v, err)
	}
}
func TestXMLBoundsAndStructure(t *testing.T) {
	cases := []struct {
		name, body string
		options    XMLOptions
		status     int
	}{
		{"empty", "", XMLOptions{}, 400}, {"malformed", "<message>", XMLOptions{}, 400}, {"roots", "<message/><message/>", XMLOptions{}, 400},
		{"trailing", "<message/>secret", XMLOptions{}, 400}, {"leading", "secret<message/>", XMLOptions{}, 400}, {"dtd", `<!DOCTYPE message><message/>`, XMLOptions{}, 400},
		{"bytes", "<message/>", XMLOptions{MaxBytes: 5}, 413}, {"depth", "<message><text/></message>", XMLOptions{MaxDepth: 1}, 413},
		{"nodes", "<message><text/><text/></message>", XMLOptions{MaxNodes: 2}, 413},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(c.body))
			r.Header.Set("Content-Type", "text/xml")
			_, err := DecodeXML[xmlInput](httptest.NewRecorder(), r, c.options)
			var h *httperror.Error
			if !errors.As(err, &h) || h.Status != c.status {
				t.Fatalf("got %v, want %d", err, c.status)
			}
		})
	}
}
func TestXMLContextAndMedia(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DecodeXMLBytes[xmlInput](ctx, []byte(`<message/>`))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<message/>`))
	_, err = ReadXML(httptest.NewRecorder(), r)
	var h *httperror.Error
	if !errors.As(err, &h) || h.Status != 415 {
		t.Fatal(err)
	}
}

func TestXMLUnknownAndSchema(t *testing.T) {
	v, err := DecodeXMLBytes[xmlInput](context.Background(), []byte(`<message><future>ignored</future></message>`))
	if err != nil || v.XMLName.Local != "message" {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err = DecodeXMLBytes[string](context.Background(), []byte(`<message/>`)); !errors.Is(err, ErrSchema) {
		t.Fatal(err)
	}
	if _, err = DecodeXMLBytes[xmlInput](context.Background(), []byte(`<message/>`), XMLOptions{MaxNodes: -1}); !errors.Is(err, ErrSchema) {
		t.Fatal(err)
	}
	if _, err = DecodeXMLBytes[xmlInput](context.Background(), []byte("<message/>\u00a0")); err == nil {
		t.Fatal("accepted non-XML whitespace outside root")
	}
}

func TestXMLNativeSchemaIsNotClientError(t *testing.T) {
	// Build the exact duplicate declaration dynamically so go vet does not
	// reject this deliberately invalid regression fixture before it runs.
	duplicate := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[string](), Tag: `xml:"same"`},
		{Name: "B", Type: reflect.TypeFor[string](), Tag: `xml:"same"`},
	})
	if err := CheckXMLSchema(duplicate); !errors.Is(err, ErrSchema) {
		t.Fatalf("duplicate tags: %v", err)
	}
	_, err := DecodeXMLBytes[struct {
		A string `xml:"B"`
		B string
	}](context.Background(), []byte(`<root/>`))
	var httpErr *httperror.Error
	if !errors.Is(err, ErrSchema) || errors.As(err, &httpErr) {
		t.Fatalf("expected schema error without HTTP 400, got %v", err)
	}
	_, err = DecodeXMLBytes[struct {
		A map[string]string `xml:"a"`
	}](context.Background(), []byte(`<root><a/></root>`))
	if !errors.Is(err, ErrSchema) || errors.As(err, &httpErr) {
		t.Fatalf("expected type schema error, got %v", err)
	}
}

var errXMLBusinessFailure = errors.New("business decoder failed")

type xmlFailingDecoder struct{}

func (*xmlFailingDecoder) UnmarshalXML(*xml.Decoder, xml.StartElement) error {
	return errXMLBusinessFailure
}

func TestXMLCustomErrorsRetainInternalClassification(t *testing.T) {
	_, err := DecodeXMLBytes[xmlFailingDecoder](context.Background(), []byte(`<root/>`))
	var httpErr *httperror.Error
	if !errors.Is(err, errXMLBusinessFailure) || errors.As(err, &httpErr) {
		t.Fatalf("expected preserved business error, got %v", err)
	}
}

func TestXMLNativeValueErrorsAreClientErrors(t *testing.T) {
	_, err := DecodeXMLBytes[xmlInput](context.Background(), []byte(`<message><nested><value>bad</value></nested></message>`))
	var httpErr *httperror.Error
	if !errors.As(err, &httpErr) || httpErr.Status != 400 {
		t.Fatalf("expected client conversion error, got %v", err)
	}
}

type xmlEmbeddedNative struct {
	Name string `xml:"name"`
}

func TestXMLNativeMultiplePointersAreSchemaErrors(t *testing.T) {
	_, err := DecodeXMLBytes[struct {
		A **string `xml:"a"`
	}](context.Background(), []byte(`<root><a>value</a></root>`))
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("expected schema error, got %v", err)
	}
}

type xmlRecursivePointer *xmlRecursivePointer

type xmlNamedByte uint8
type xmlNamedBytes []byte
type xmlHiddenEmbedding struct {
	Value string `xml:"value"`
}

func TestXMLRejectsUnsafeNativeAssignments(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			Comment any `xml:",comment"`
		}](),
		reflect.TypeFor[struct {
			Raw any `xml:",innerxml"`
		}](),
		reflect.TypeFor[struct {
			Comment []xmlNamedByte `xml:",comment"`
		}](),
		reflect.TypeFor[struct {
			Raw []xmlNamedByte `xml:",innerxml"`
		}](),
		reflect.TypeFor[struct{ *xmlHiddenEmbedding }](),
	} {
		if err := CheckXMLSchema(typ); !errors.Is(err, ErrSchema) {
			t.Errorf("unsafe declaration accepted: %s", typ)
		}
	}
}

func TestXMLPreservesNamedByteSliceAssignments(t *testing.T) {
	v, err := DecodeXMLBytes[struct {
		Comment xmlNamedBytes `xml:",comment"`
		Raw     xmlNamedBytes `xml:",innerxml"`
	}](context.Background(), []byte(`<root><!--note--><value>x</value></root>`))
	if err != nil || string(v.Comment) != "note" || string(v.Raw) != "<!--note--><value>x</value>" {
		t.Fatalf("named byte slice mapping failed: %+v %v", v, err)
	}
}

func TestXMLRecursiveNamedPointerIsSchemaError(t *testing.T) {
	if err := CheckXMLSchema(reflect.TypeFor[struct {
		A xmlRecursivePointer `xml:"a"`
	}]()); !errors.Is(err, ErrSchema) {
		t.Fatalf("recursive pointer: %v", err)
	}
}

type xmlRecursiveSlice []xmlRecursiveSlice
type xmlRecursiveSliceA []xmlRecursiveSliceB
type xmlRecursiveSliceB []xmlRecursiveSliceA

func TestXMLRecursiveSlicesAreSchemaErrors(t *testing.T) {
	// Keep a regression from exhausting the test runner's stack. Native slice
	// recursion consumes no token, so these declarations must fail preparation.
	const helper = "FRAME_XML_RECURSIVE_SLICE_TEST"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestXMLRecursiveSlicesAreSchemaErrors$")
		cmd.Env = append(os.Environ(), helper+"=1")
		output, err := cmd.CombinedOutput()
		if len(output) > 1500 {
			output = output[:1500]
		}
		if err != nil {
			t.Fatalf("recursive schema check: %v\n%s", err, output)
		}
		return
	}
	debug.SetMaxStack(64 << 10)
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			A xmlRecursiveSlice `xml:"a"`
		}](),
		reflect.TypeFor[struct {
			A xmlRecursiveSliceA `xml:"a"`
		}](),
	} {
		if err := CheckXMLSchema(typ); !errors.Is(err, ErrSchema) {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}

type xmlRecursiveTree struct {
	Children []xmlRecursiveTree `xml:"child"`
}

func TestXMLRecursiveStructCollectionRemainsSupported(t *testing.T) {
	v, err := DecodeXMLBytes[xmlRecursiveTree](context.Background(), []byte(`<root><child><child/></child></root>`))
	if err != nil || len(v.Children) != 1 || len(v.Children[0].Children) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestXMLSchemaPreservesUnexportedNativeEmbedding(t *testing.T) {
	v, err := DecodeXMLBytes[struct {
		xmlEmbeddedNative
		Name string `xml:"name"`
	}](context.Background(), []byte(`<root><name>outer</name></root>`))
	if err != nil || v.Name != "outer" || v.xmlEmbeddedNative.Name != "" {
		t.Fatalf("%+v %v", v, err)
	}
}
