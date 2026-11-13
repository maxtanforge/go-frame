package request_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/optional"
)

func TestLowLevelDecodersUseCanonicalID(t *testing.T) {
	type DTO struct {
		ID id.ID `query:"id" path:"id"`
	}
	for _, value := range []string{"9007199254740993", "01", "-1", "+1"} {
		_, queryErr := request.DecodeQuery[DTO](url.Values{"id": {value}}.Encode(), DTO{})
		_, pathErr := request.DecodePath[DTO](map[string]string{"id": value})
		valid := value == "9007199254740993"
		if (queryErr == nil) != valid || (pathErr == nil) != valid {
			t.Fatalf("%q: %v %v", value, queryErr, pathErr)
		}
	}
}

func TestPreparedIDAcrossSources(t *testing.T) {
	type DTO struct{ ID id.ID }
	for _, source := range []request.Source{request.JSON, request.Query, request.Path, request.Form} {
		p, err := request.Prepare[DTO](request.Options{Source: source})
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"9007199254740993", "0", "01", "-1", "+1", "9223372036854775808"} {
			r := httptest.NewRequest("POST", "/?id="+strings.ReplaceAll(value, "+", "%2B"), strings.NewReader(`{"id":"`+value+`"}`))
			r.Header.Set("Content-Type", "application/json")
			if source == request.Form {
				r = httptest.NewRequest("POST", "/", strings.NewReader("id="+strings.ReplaceAll(value, "+", "%2B")))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			got, _, err := p.Decode(httptest.NewRecorder(), r, map[string]string{"id": value})
			valid := value == "9007199254740993" || value == "0"
			if (err == nil) != valid {
				t.Fatalf("%s %q: %v", source, value, err)
			}
			if valid && value != "0" && got.ID.Int64() != 9007199254740993 {
				t.Fatalf("precision lost: %d", got.ID)
			}
		}
	}
}

func TestPreparedIDNestedOptionalAndNumericRejection(t *testing.T) {
	type Child struct{ ID id.ID }
	type DTO struct {
		Child   Child
		IDs     []id.ID
		Pointer *id.ID
		Patch   optional.Field[id.ID]
	}
	p, err := request.Prepare[DTO](request.Options{Source: request.JSON})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"child":{"id":"9007199254740993"},"ids":["1","2"],"pointer":"3","patch":"4"}`, `{"child":{"id":1}}`, `{"ids":["01"]}`, `{"pointer":1}`, `{"patch":"-1"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, _, err := p.Decode(httptest.NewRecorder(), r, nil)
		if (err == nil) != strings.Contains(body, "9007199254740993") {
			t.Fatalf("%s: %v", body, err)
		}
	}
}
