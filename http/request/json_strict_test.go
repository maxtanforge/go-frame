package request_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/stretchr/testify/require"
)

func TestJSONEntrypointsRejectAmbiguousAndUnboundedStructure(t *testing.T) {
	type Child struct {
		Count int    `json:"count"`
		Name  string `json:"name"`
	}
	type DTO struct {
		Child Child `json:"child"`
	}
	p, err := request.Prepare[DTO](request.Options{Source: request.JSON})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"duplicate", `{"child":{},"child":{}}`, 400},
		{"escaped duplicate", `{"child":{},"\u0063hild":{}}`, 400},
		{"nested duplicate", `{"child":{"count":1,"count":2}}`, 400},
		{"invalid UTF-8", "{\"child\":{\"name\":\"\xff\"}}", 400},
		{"depth", `{"child":` + strings.Repeat("[", 16) + "0" + strings.Repeat("]", 16) + "}", 413},
		{"items", `{"child":[` + strings.Repeat("0,", 1000) + "0]}", 413},
		{"fields", `{"child":{` + manyJSONFields(65) + "}}", 413},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/low", true: "/prepared"}[prepared], func(t *testing.T) {
				r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
				r.Header.Set("Content-Type", "application/json")
				if prepared {
					_, _, err = p.Decode(httptest.NewRecorder(), r, nil)
				} else {
					_, err = request.DecodeJSON[DTO](httptest.NewRecorder(), r)
				}
				var app *httperror.Error
				require.True(t, errors.As(err, &app), "%v", err)
				require.Equal(t, tc.status, app.Status)
				require.NotContains(t, app.Message, "child")
			})
		}
	}
}

func TestLowJSONRejectsInvalidUTF8BeforeCustomDecode(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("\"\xff\""))
	r.Header.Set("Content-Type", "application/json")
	_, err := request.DecodeJSON[customScalar](httptest.NewRecorder(), r)
	var app *httperror.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, 400, app.Status)
}

func manyJSONFields(n int) string {
	fields := make([]string, n)
	for i := range fields {
		fields[i] = `"` + strings.Repeat("x", i+1) + `":0`
	}
	return strings.Join(fields, ",")
}

type customScalar int

func (v *customScalar) UnmarshalJSON([]byte) error { *v = 42; return nil }

func TestLowJSONPreservesScalarAndCustomDecoder(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`123`))
	r.Header.Set("Content-Type", "application/json")
	value, err := request.DecodeJSON[customScalar](httptest.NewRecorder(), r)
	require.NoError(t, err)
	require.Equal(t, customScalar(42), value)
}

func TestLowJSONStructureLimitsAreConfigurable(t *testing.T) {
	for _, tc := range []struct {
		body    string
		options request.DecodeOptions
		status  int
	}{
		{`{"items":[0]}`, request.DecodeOptions{MaxDepth: 2, MaxItems: 1, MaxFields: 1}, 0},
		{`{"items":[0,1]}`, request.DecodeOptions{MaxItems: 1}, 413},
		{`{"items":[[0]]}`, request.DecodeOptions{MaxDepth: 2}, 413},
		{`{"a":0,"b":0}`, request.DecodeOptions{MaxFields: 1}, 413},
		{`{"items":[0]}`, request.DecodeOptions{MaxDepth: -1}, 500},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		_, err := request.DecodeJSON[map[string]any](httptest.NewRecorder(), r, tc.options)
		if tc.status == 0 {
			require.NoError(t, err)
			continue
		}
		if tc.status == 500 {
			require.ErrorIs(t, err, request.ErrSchema)
			continue
		}
		var app *httperror.Error
		require.ErrorAs(t, err, &app)
		require.Equal(t, tc.status, app.Status)
	}
}
