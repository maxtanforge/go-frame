package profilehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/profile"
	"github.com/maxtanforge/go-frame/examples/business/internal/profile/upstream"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

type permission struct {
	actor, target string
	allow         bool
	err           error
	calls         int
}

func (p *permission) CanEdit(_ context.Context, a, b string) (bool, error) {
	p.actor = a
	p.target = b
	p.calls++
	return p.allow, p.err
}

type repository struct {
	model  profile.Profile
	writes int
}

func (r *repository) Update(ctx context.Context, _ string, f func(*profile.Profile) error) (profile.Profile, error) {
	next := r.model
	if err := f(&next); err != nil {
		return profile.Profile{}, err
	}
	if err := ctx.Err(); err != nil {
		return profile.Profile{}, err
	}
	r.model = next
	r.writes++
	return next, nil
}
func request(t *testing.T, body, actor string, p profile.Authorizer, repo *repository) *httptest.ResponseRecorder {
	t.Helper()
	routes, err := Routes(profile.Service{Authorizer: p, Repository: repo})
	require.NoError(t, err)
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Routes: routes, Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{ID: actor, Issuer: "test"}, nil
	})})
	require.NoError(t, err)
	req := httptest.NewRequest("PATCH", "/profiles/bob", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set("Authorization", "Bearer test")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type upstreamDoFunc func(*http.Request) (*http.Response, error)

func (f upstreamDoFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestComposedUpstreamPermissionAndHTTPPatch(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"success":true,"data":{"allowed":true}}`, 200},
		{`{"success":true,"data":{"allowed":false}}`, 403},
		{`{"success":false,"message":"private detail"}`, 502},
		{`{"success":true,"data":{}}`, 502},
	} {
		t.Run(tc.body, func(t *testing.T) {
			calls := 0
			client, err := upstream.New("https://permissions.example", upstreamDoFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "Bearer service-token", r.Header.Get("Authorization"))
				require.Equal(t, "/permissions/profile/edit", r.URL.Path)
				var body map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, map[string]string{"actor_id": "alice", "target_id": "bob"}, body)
				actor, ok := principal.FromContext(r.Context())
				require.True(t, ok)
				require.Equal(t, "alice", actor.ID)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}), func(context.Context) (string, error) { return "service-token", nil })
			require.NoError(t, err)
			repo := &repository{model: profile.Profile{ID: "bob", Basic: profile.Basic{DisplayName: "old", Age: 9}, Note: "keep", HasNote: true}}
			w := request(t, `{"basic":{"age":0}}`, "alice", client, repo)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, 1, calls)
			require.NotContains(t, w.Body.String(), "private detail")
			if tc.status == 200 {
				require.Equal(t, 1, repo.writes)
				require.Zero(t, repo.model.Basic.Age)
				require.Equal(t, "old", repo.model.Basic.DisplayName)
			} else {
				require.Zero(t, repo.writes)
				require.Equal(t, 9, repo.model.Basic.Age)
			}
		})
	}
}
func TestAuthenticatedNestedPatch(t *testing.T) {
	for _, tc := range []struct {
		body  string
		basic profile.Basic
		note  string
		has   bool
	}{
		{`{"basic":{"display_name":""}}`, profile.Basic{Age: 9}, "keep", true},
		{`{"basic":{"age":0}}`, profile.Basic{DisplayName: "old"}, "keep", true},
		{`{"note":null}`, profile.Basic{DisplayName: "old", Age: 9}, "", false},
		{`{"note":""}`, profile.Basic{DisplayName: "old", Age: 9}, "", true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			p := &permission{allow: true}
			repo := &repository{model: profile.Profile{ID: "bob", Basic: profile.Basic{DisplayName: "old", Age: 9}, Note: "keep", HasNote: true}}
			w := request(t, tc.body, "alice", p, repo)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, "alice", p.actor)
			require.Equal(t, "bob", p.target)
			require.Equal(t, tc.basic, repo.model.Basic)
			require.Equal(t, tc.note, repo.model.Note)
			require.Equal(t, tc.has, repo.model.HasNote)
			require.NotContains(t, w.Body.String(), "HasNote")
		})
	}
}
func TestInvalidTransportAndNoOp(t *testing.T) {
	for _, body := range []string{`{}`, `{"basic":{}}`, `{"basic":null}`, `{"basic":{"age":null}}`, `{"basic":{"display_name":null}}`, `{"basic":1}`, `{"basic":{"age":-1}}`, `{"basic":{"age":151}}`, `{"basic":{"unknown":1}}`, `{"actor":"admin","note":"x"}`, `{"id":"alice","note":"x"}`, `{"note":4}`} {
		t.Run(body, func(t *testing.T) {
			p := &permission{allow: true}
			repo := &repository{}
			w := request(t, body, "alice", p, repo)
			require.Equal(t, 400, w.Code, w.Body.String())
			require.Zero(t, repo.writes)
		})
	}
}
func TestAuthenticationPermissionAndDependencyErrors(t *testing.T) {
	for _, tc := range []struct {
		actor  string
		allow  bool
		err    error
		status int
	}{{"", true, nil, 401}, {"alice", false, nil, 403}, {"alice", false, errors.New("secret upstream credential"), 502}, {"alice", false, profile.ErrForbidden, 502}, {"alice", false, profile.ErrInvalid, 502}, {"alice", false, context.Canceled, 502}, {"alice", false, context.DeadlineExceeded, 502}} {
		p := &permission{allow: tc.allow, err: tc.err}
		repo := &repository{}
		w := request(t, `{"note":"x"}`, tc.actor, p, repo)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		require.Zero(t, repo.writes)
		require.NotContains(t, w.Body.String(), "secret")
		if tc.actor == "" {
			require.Zero(t, p.calls)
		} else {
			require.Equal(t, "bob", p.target)
		}
	}
}
