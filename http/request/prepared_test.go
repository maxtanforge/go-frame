package request_test

import (
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/validation"
)

func TestPreparedQueryPresence(t *testing.T) {
	type DTO struct {
		UserID  int64
		Current *int `d:"2"`
		Active  bool
		IDs     []uint8
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.Query})
	if e != nil {
		t.Fatal(e)
	}
	got, s, e := p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/?user_id=9007199254740993&active=false&ids=1&ids=2", nil), nil)
	if e != nil || got.UserID != 9007199254740993 || got.Current == nil || *got.Current != 2 || !s["Active"].Present || s["Current"].Present || !s["Current"].Decoded {
		t.Fatalf("%+v %+v %v", got, s, e)
	}
	_, _, e = p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/?user_id=no&active=1", nil), nil)
	var es *validation.Errors
	if !errors.As(e, &es) || len(es.Issues()) != 2 {
		t.Fatalf("%v", e)
	}
}
func TestPreparedJSONStrictAndNested(t *testing.T) {
	type Child struct {
		Count int `d:"3"`
	}
	type DTO struct {
		Child *Child
		Items []Child
		Patch optional.Field[string] `nullable:"true"`
		At    time.Time
		ID    int64 `json:",string"`
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.JSON})
	if e != nil {
		t.Fatal(e)
	}
	decode := func(body string) (DTO, map[string]validation.FieldState, error) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		return p.Decode(httptest.NewRecorder(), r, nil)
	}
	got, s, e := decode(`{"child":{},"items":[{}],"patch":null,"at":1234,"id":"9007199254740993"}`)
	if e != nil || got.Child.Count != 3 || got.Items[0].Count != 3 || !got.Patch.Null || got.At.UnixMilli() != 1234 || !s["Items[0].Count"].Decoded {
		t.Fatalf("%+v %+v %v", got, s, e)
	}
	for _, raw := range []string{`{"child":{},"child":{}}`, `{"unknown":"secret"}`, `{"at":"123"}`, `{"id":123}`, `{"items":null}`} {
		_, _, e = decode(raw)
		if e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestPreparedSchema(t *testing.T) {
	type Bad struct {
		X int `validate:"required"`
	}
	_, e := request.Prepare[Bad](request.Options{Source: request.Query})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatal(e)
	}
	type Recursive struct{ Next *Recursive }
	_, e = request.Prepare[Recursive](request.Options{Source: request.JSON})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatal(e)
	}
}

func TestPreparedDefaultsAreIndependent(t *testing.T) {
	type DTO struct {
		N *int `d:"4"`
		B bool `d:"true"`
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.Query})
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), nil)
	if e != nil {
		t.Fatal(e)
	}
	*a.N = 9
	b, s, e := p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/?b=false", nil), nil)
	if e != nil || *b.N != 4 || b.B || !s["B"].Present {
		t.Fatalf("%+v %v", b, e)
	}
	_, _, e = p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/?n=", nil), nil)
	if e == nil {
		t.Fatal("empty numeric accepted")
	}
	fields := p.Fields()
	fields[0].Path[0] = "modified"
	fields[0].Index[0] = 99
	if p.Fields()[0].Path[0] != "n" || p.Fields()[0].Index[0] != 0 {
		t.Fatal("mutable metadata")
	}
}
func TestPreparedPath(t *testing.T) {
	type DTO struct{ UserID uint64 }
	p, e := request.Prepare[DTO](request.Options{Source: request.Path})
	if e != nil {
		t.Fatal(e)
	}
	got, _, e := p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), map[string]string{"user_id": "18446744073709551615"})
	if e != nil || got.UserID != ^uint64(0) {
		t.Fatalf("%+v %v", got, e)
	}
	_, _, e = p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), nil)
	if !errors.Is(e, validation.ErrInvalid) {
		t.Fatal(e)
	}
}
func TestPreparedLimitsAndNull(t *testing.T) {
	type Child struct {
		N int `d:"3"`
	}
	type DTO struct {
		Child  *Child
		Values []int
		Patch  optional.Field[string]
		N      int
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.JSON, MaxItems: 2, MaxDepth: 2, MaxBytes: 100})
	if e != nil {
		t.Fatal(e)
	}
	decode := func(raw string) (DTO, map[string]validation.FieldState, error) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		return p.Decode(httptest.NewRecorder(), r, nil)
	}
	got, _, e := decode(`{}`)
	if e != nil || got.Child != nil {
		t.Fatalf("absent parent created: %+v %v", got, e)
	}
	got, s, e := decode(`{"child":null,"patch":null,"n":null}`)
	var es *validation.Errors
	if !errors.As(e, &es) || len(es.Issues()) != 2 || !got.Patch.Present || !got.Patch.Null || !s["N"].Invalid {
		t.Fatalf("%+v %+v %v", got, s, e)
	}
	for _, raw := range []string{`{"values":[1,2,3]}`, `{"child":{"n":{"x":1}}}`, strings.Repeat(" ", 101), `{} {}`, `{"n":1,"n":2}`} {
		_, _, e = decode(raw)
		if e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestPreparedDeclarationRejections(t *testing.T) {
	checks := []func() error{
		func() error {
			_, e := request.Prepare[struct {
				A int `d:"bad"`
			}](request.Options{Source: request.Query})
			return e
		},
		func() error {
			_, e := request.Prepare[struct {
				A int `d:"1" v:"present"`
			}](request.Options{Source: request.JSON})
			return e
		},
		func() error {
			_, e := request.Prepare[struct {
				A int `nullable:"true"`
			}](request.Options{Source: request.JSON})
			return e
		},
		func() error {
			_, e := request.Prepare[struct {
				A int `json:"-" v:"min=1"`
			}](request.Options{Source: request.JSON})
			return e
		},
		func() error {
			_, e := request.Prepare[struct {
				A int
				B int `json:"a"`
			}](request.Options{Source: request.JSON})
			return e
		},
		func() error {
			_, e := request.Prepare[struct{ A time.Duration }](request.Options{Source: request.JSON})
			return e
		},
		func() error {
			_, e := request.Prepare[struct{ A map[string]string }](request.Options{Source: request.JSON})
			return e
		},
	}
	for i, check := range checks {
		if e := check(); !errors.Is(e, request.ErrSchema) {
			t.Fatalf("case %d: %v", i, e)
		}
	}
}

func TestPreparedIssueOrder(t *testing.T) {
	type Child struct {
		Z int
		A int
	}
	type DTO struct {
		Z     int
		Items []Child
		A     int
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.JSON})
	if e != nil {
		t.Fatal(e)
	}
	issues := []validation.Issue{{Path: validation.Path{"a"}, Code: "type"}, {Path: validation.Path{"items", 1, "z"}, Code: "type"}, {Path: validation.Path{"z"}, Code: "min"}, {Path: validation.Path{"items", 0, "a"}, Code: "type"}, {Path: validation.Path{"items", 0, "z"}, Code: "min"}}
	got := p.OrderIssues(issues)
	want := []validation.Path{{"z"}, {"items", 0, "z"}, {"items", 0, "a"}, {"items", 1, "z"}, {"a"}}
	for i := range got {
		if !reflect.DeepEqual(got[i].Path, want[i]) {
			t.Fatalf("%+v", got)
		}
	}
	got[0].Path[0] = "changed"
	if issues[2].Path[0] != "z" {
		t.Fatal("input mutated")
	}
}

func TestPreparedRejectsDurationCollections(t *testing.T) {
	_, e := request.Prepare[struct{ Times []time.Duration }](request.Options{Source: request.JSON})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatalf("duration collection accepted: %v", e)
	}
}

type PreparedPage struct {
	Current int `d:"1"`
}

func TestPreparedEmbeddedAndNames(t *testing.T) {
	type DTO struct {
		PreparedPage
		HTTPCode int
		UserID   int `json:"uid" query:"id"`
	}
	p, e := request.Prepare[DTO](request.Options{Source: request.Query})
	if e != nil {
		t.Fatal(e)
	}
	got, s, e := p.Decode(httptest.NewRecorder(), httptest.NewRequest("GET", "/?http_code=200&id=7", nil), nil)
	if e != nil || got.Current != 1 || got.HTTPCode != 200 || got.UserID != 7 || !s["PreparedPage.Current"].Decoded {
		t.Fatalf("%+v %+v %v", got, s, e)
	}
}
func TestPreparedInvalidLabelAtStartup(t *testing.T) {
	_, e := request.Prepare[struct {
		N int `label:"bad\nlabel"`
	}](request.Options{Source: request.JSON})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatalf("invalid label accepted: %v", e)
	}
}

func TestPreparedRejectsRulesOnImplicitEmbed(t *testing.T) {
	_, e := request.Prepare[struct {
		PreparedPage `v:"required"`
	}](request.Options{Source: request.JSON})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatalf("flattened embed rules silently ignored: %v", e)
	}
}

type PreparedCustomEmbed struct{ N int }

func (*PreparedCustomEmbed) Validate(*validation.Context) {}
func TestPreparedRejectsCustomImplicitEmbed(t *testing.T) {
	_, e := request.Prepare[struct{ PreparedCustomEmbed }](request.Options{Source: request.JSON})
	if !errors.Is(e, request.ErrSchema) {
		t.Fatalf("custom embedded method accepted: %v", e)
	}
}
