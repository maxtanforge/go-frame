package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type permissionFunc func(context.Context, string, string) (bool, error)

func (f permissionFunc) CanEdit(c context.Context, a, b string) (bool, error) { return f(c, a, b) }

type memory struct {
	model  Profile
	writes int
}

func (m *memory) Update(ctx context.Context, _ string, change func(*Profile) error) (Profile, error) {
	next := m.model
	if err := change(&next); err != nil {
		return Profile{}, err
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	m.model = next
	m.writes++
	return next, nil
}
func TestPatchAuthorizesEveryPairAndAppliesExplicitChanges(t *testing.T) {
	for _, target := range []string{"alice", "bob"} {
		t.Run(target, func(t *testing.T) {
			repo := &memory{model: Profile{ID: target, Basic: Basic{DisplayName: "old", Age: 9}, Note: "keep", HasNote: true}}
			calls := 0
			svc := Service{Repository: repo, Authorizer: permissionFunc(func(_ context.Context, a, b string) (bool, error) {
				calls++
				require.Equal(t, "alice", a)
				require.Equal(t, target, b)
				return true, nil
			})}
			got, err := svc.Patch(context.Background(), "alice", target, PatchCommand{Basic: BasicChange{DisplayName: TextChange{Set: true, Value: ""}, Age: IntChange{Set: true, Value: 0}}})
			require.NoError(t, err)
			require.Equal(t, Basic{}, got.Basic)
			require.Equal(t, "keep", got.Note)
			require.Equal(t, 1, calls)
			got, err = svc.Patch(context.Background(), "alice", target, PatchCommand{Note: NoteChange{Clear: true}})
			require.NoError(t, err)
			require.False(t, got.HasNote)
			require.Empty(t, got.Note)
		})
	}
}
func TestPatchRejectsBeforeMutation(t *testing.T) {
	fault := errors.New("secret upstream failure")
	for _, tc := range []struct {
		name, actor string
		cmd         PatchCommand
		allow       bool
		fault, want error
	}{
		{"denied", "alice", PatchCommand{Note: NoteChange{Clear: true}}, false, nil, ErrForbidden},
		{"dependency", "alice", PatchCommand{Note: NoteChange{Clear: true}}, false, fault, ErrDependency},
		{"dependency with denial cause", "alice", PatchCommand{Note: NoteChange{Clear: true}}, false, ErrForbidden, ErrDependency},
		{"no actor", "", PatchCommand{Note: NoteChange{Clear: true}}, true, nil, ErrUnauthenticated},
		{"noop", "alice", PatchCommand{}, true, nil, ErrInvalid},
		{"negative age", "alice", PatchCommand{Basic: BasicChange{Age: IntChange{Set: true, Value: -1}}}, true, nil, ErrInvalid},
		{"conflicting note", "alice", PatchCommand{Note: NoteChange{Set: true, Clear: true}}, true, nil, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &memory{}
			svc := Service{Repository: repo, Authorizer: permissionFunc(func(context.Context, string, string) (bool, error) { return tc.allow, tc.fault })}
			_, err := svc.Patch(context.Background(), tc.actor, "bob", tc.cmd)
			require.ErrorIs(t, err, tc.want)
			if tc.fault != nil {
				require.ErrorIs(t, err, tc.fault)
				require.NotContains(t, err.Error(), "secret")
			}
			require.Zero(t, repo.writes)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	repo := &memory{}
	svc := Service{Repository: repo, Authorizer: permissionFunc(func(context.Context, string, string) (bool, error) { cancel(); return true, nil })}
	_, err := svc.Patch(ctx, "alice", "bob", PatchCommand{Note: NoteChange{Clear: true}})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, repo.writes)
}
