package request

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
)

func TestPreparedForm(t *testing.T) {
	type Input struct {
		Current int `d:"1" v:"gte=1"`
		Active  *bool
		Names   []string
	}
	p, err := Prepare[Input](Options{Source: Form})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/?current=99", strings.NewReader("active=false&names=a&names=b"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got, states, err := p.Decode(httptest.NewRecorder(), r, nil)
	if err != nil || got.Current != 1 || got.Active == nil || *got.Active || len(got.Names) != 2 || states["Current"].Present {
		t.Fatalf("%+v %v %v", got, states, err)
	}
	for _, body := range []string{"current=", "current=1&current=2", "unknown=x", "%zz=1"} {
		r = httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, _, err = p.Decode(httptest.NewRecorder(), r, nil); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}

func TestFormLimits(t *testing.T) {
	p, _ := Prepare[struct{ Name string }](Options{Source: Form, MaxBytes: 8})
	for _, tc := range []struct {
		media, body string
		status      int
	}{{"text/plain", "name=x", 415}, {"application/x-www-form-urlencoded", "name=long", 413}} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.media)
		_, _, err := p.Decode(httptest.NewRecorder(), r, nil)
		var h *httperror.Error
		if !errors.As(err, &h) || h.Status != tc.status {
			t.Fatalf("%v", err)
		}
	}
}

func TestFormFieldCountLimit(t *testing.T) {
	p, _ := Prepare[struct{ Name string }](Options{Source: Form, MaxFields: 1})
	r := httptest.NewRequest("POST", "/", strings.NewReader("name=a&other=b"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, _, err := p.Decode(httptest.NewRecorder(), r, nil)
	var h *httperror.Error
	if !errors.As(err, &h) || h.Status != 413 {
		t.Fatalf("expected 413, got %v", err)
	}
}
