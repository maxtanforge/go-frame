package respond

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/stretchr/testify/require"
)

func TestChildDeadlineRespectsApplicationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"bare", context.DeadlineExceeded, 500, msgProgram},
		{"wrapped", fmt.Errorf("database budget: %w", context.DeadlineExceeded), 500, msgProgram},
		{"application", &httperror.Error{Status: 503, Message: "try again", Err: context.DeadlineExceeded}, 503, "try again"},
		{"joined application", errors.Join(context.DeadlineExceeded, &httperror.Error{Status: 409, Message: "conflict"}), 409, "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &strings.Builder{}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/", nil).WithContext(ctxLogger(logs))
			WriteError(w, r, tc.err)
			require.Equal(t, tc.status, w.Code)
			require.Contains(t, w.Body.String(), tc.message)
			require.NotContains(t, w.Body.String(), "context deadline exceeded")
			require.Contains(t, logs.String(), "context deadline exceeded")
		})
	}
}
