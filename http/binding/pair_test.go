package binding

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type pairPath struct {
	ID int `v:"gte=1"`
}
type pairQuery struct {
	Limit int `v:"gte=1"`
}

func TestPairPreservesCompletePathThenQueryIssues(t *testing.T) {
	p, err := newDecoder[pairPath](request.Path, Options{PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "0"} }})
	require.NoError(t, err)
	q, err := newDecoder[pairQuery](request.Query)
	require.NoError(t, err)
	_, _, err = decodePair(p, q, httptest.NewRecorder(), httptest.NewRequest("GET", "/?limit=0", nil))
	var issues *validation.Errors
	require.ErrorAs(t, err, &issues)
	require.Len(t, issues.Issues(), 2)
	require.Equal(t, validation.Path{"id"}, issues.Issues()[0].Path)
	require.Equal(t, validation.Path{"limit"}, issues.Issues()[1].Path)
}

type hardPath struct{ ID int }

func (*hardPath) Validate(c *validation.Context) {
	c.Merge(&httperror.Error{Status: 500, Message: "internal", Err: errors.New("stop")})
}

type panicQuery struct{ Limit int }

func (*panicQuery) Validate(*validation.Context) { panic("query must not run") }
func TestPairHardPathErrorStopsSecondDecoder(t *testing.T) {
	p, err := newDecoder[hardPath](request.Path, Options{PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "1"} }})
	require.NoError(t, err)
	q, err := newDecoder[panicQuery](request.Query)
	require.NoError(t, err)
	require.NotPanics(t, func() {
		_, _, err = decodePair(p, q, httptest.NewRecorder(), httptest.NewRequest("GET", "/?limit=1", nil))
	})
	var httpErr *httperror.Error
	require.ErrorAs(t, err, &httpErr)
}
