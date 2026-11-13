package transactionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

type captureService struct {
	calls   int
	actor   string
	query   Query
	failure error
	empty   bool
}

func (s *captureService) List(ctx context.Context, actor string, q Query) (Result, error) {
	s.calls++
	s.actor, s.query = actor, q
	p, ok := principal.FromContext(ctx)
	if !ok || p.ID != actor {
		return Result{}, errors.New("missing verified actor")
	}
	if s.empty {
		return Result{}, s.failure
	}
	return Result{Records: []Transaction{{ID: 9223372036854775807, Amount: 1250, Owner: "private owner"}}, Total: 201}, s.failure
}

func authenticated(t *testing.T, build func(Service) ([]router.Route, error), s Service) *router.Authenticated {
	t.Helper()
	routes, err := build(s)
	require.NoError(t, err)
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
		Router: router.Options{DisableHealthMount: true}, Routes: routes,
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{ID: "alice", Issuer: "test"}, nil
		}),
	})
	require.NoError(t, err)
	return r
}

func serve(t *testing.T, r *router.Authenticated, path string, authorized bool) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if authorized {
		req.Header.Set("Authorization", "Bearer test")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope["traceId"])
	require.NotZero(t, envelope["timestamp"])
	delete(envelope, "traceId")
	delete(envelope, "timestamp")
	return w.Code, envelope
}

func TestPaginationDeclarationsShareAuthenticatedContract(t *testing.T) {
	for _, tc := range []struct {
		name, path, sort     string
		current, size, pages int64
		empty                bool
	}{
		{"defaults", "/accounts/9223372036854775807/transactions", "created_at", 1, 20, 11, false},
		{"explicit", "/accounts/9223372036854775807/transactions?current=2&size=100&sort=amount", "amount", 2, 100, 3, false},
		{"minimum", "/accounts/9223372036854775807/transactions?current=1&size=1", "created_at", 1, 1, 201, false},
		{"empty", "/accounts/9223372036854775807/transactions", "created_at", 1, 20, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var baseline map[string]any
			var policies []router.RoutePolicy
			var query Query
			for i, build := range []func(Service) ([]router.Route, error){Routes, TypedRoutes} {
				s := &captureService{empty: tc.empty}
				r := authenticated(t, build, s)
				status, envelope := serve(t, r, tc.path, true)
				require.Equal(t, 200, status, envelope)
				require.Equal(t, 1, s.calls)
				require.Equal(t, "alice", s.actor)
				require.EqualValues(t, 9223372036854775807, s.query.Account)
				require.Equal(t, tc.current, s.query.Window.Current())
				require.Equal(t, tc.size, s.query.Window.Size())
				require.Equal(t, (tc.current-1)*tc.size, s.query.Window.Offset())
				require.Equal(t, tc.sort, s.query.Sort)
				page := envelope["data"].(map[string]any)
				require.Len(t, page, 5)
				require.EqualValues(t, tc.current, page["current"])
				require.EqualValues(t, tc.size, page["size"])
				require.EqualValues(t, tc.pages, page["pages"])
				records := page["records"].([]any)
				if tc.empty {
					require.Empty(t, records)
					require.EqualValues(t, 0, page["total"])
				} else {
					require.EqualValues(t, 201, page["total"])
					require.Len(t, records, 1)
					require.Equal(t, map[string]any{"id": "9223372036854775807", "amount": float64(1250)}, records[0])
				}
				require.Len(t, r.Policies(), 1)
				policy := r.Policies()[0]
				require.False(t, policy.Public)
				require.Equal(t, "GET", policy.Method)
				require.Equal(t, "/accounts/{account}/transactions", policy.Path)
				require.Equal(t, "transaction.list", policy.Operation.Action)
				if i == 0 {
					baseline, policies, query = envelope, r.Policies(), s.query
				} else {
					require.Equal(t, baseline, envelope)
					require.Equal(t, policies, r.Policies())
					require.Equal(t, query, s.query)
				}
			}
		})
	}
}

func TestPaginationRejectsInputBeforeService(t *testing.T) {
	for _, build := range []func(Service) ([]router.Route, error){Routes, TypedRoutes} {
		s := &captureService{}
		r := authenticated(t, build, s)
		for _, path := range []string{
			"/accounts/01/transactions", "/accounts/+1/transactions", "/accounts/0/transactions", "/accounts/-1/transactions", "/accounts/9223372036854775808/transactions",
			"/accounts/1/transactions?current=0", "/accounts/1/transactions?size=0", "/accounts/1/transactions?size=101", "/accounts/1/transactions?current=1000002&size=1",
			"/accounts/1/transactions?size=", "/accounts/1/transactions?sort=", "/accounts/1/transactions?sort=owner",
		} {
			status, envelope := serve(t, r, path, true)
			require.Equal(t, 400, status, envelope)
			require.Zero(t, s.calls, path)
		}
		status, envelope := serve(t, r, "/accounts/1/transactions?actor=alice", false)
		require.Equal(t, 401, status, envelope)
		require.Zero(t, s.calls)
	}
}

func TestPaginationServiceErrorsPreserveCauseAndStayPrivate(t *testing.T) {
	for _, tc := range []struct {
		cause  error
		status int
	}{
		{ErrInvalid, 400}, {principal.ErrUnauthenticated, 401}, {ErrForbidden, 403}, {ErrNotFound, 404}, {errors.New("private database password"), 500},
	} {
		cause := fmt.Errorf("wrapped: %w", tc.cause)
		mapped := httpError(cause)
		var public *httperror.Error
		require.ErrorAs(t, mapped, &public)
		require.Equal(t, tc.status, public.Status)
		require.ErrorIs(t, mapped, tc.cause)
		var baseline map[string]any
		for i, build := range []func(Service) ([]router.Route, error){Routes, TypedRoutes} {
			s := &captureService{failure: cause}
			status, envelope := serve(t, authenticated(t, build, s), "/accounts/1/transactions", true)
			require.Equal(t, tc.status, status, envelope)
			require.EqualValues(t, public.Code, envelope["code"])
			require.Equal(t, public.Message, envelope["message"])
			require.False(t, envelope["success"].(bool))
			require.Nil(t, envelope["data"])
			require.NotContains(t, fmt.Sprint(envelope), "password")
			require.NotContains(t, fmt.Sprint(envelope), "wrapped:")
			if i == 0 {
				baseline = envelope
			} else {
				require.Equal(t, baseline, envelope)
			}
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		require.Equal(t, cause, httpError(cause))
	}
}

func TestPaginationNilServiceFailsAtStartup(t *testing.T) {
	var typedNil *captureService
	for _, build := range []func(Service) ([]router.Route, error){Routes, TypedRoutes} {
		for _, service := range []Service{nil, typedNil} {
			routes, err := build(service)
			require.Error(t, err)
			require.Nil(t, routes)
		}
	}
}
