package entdb_test

import (
	"context"
	"github.com/maxtanforge/go-frame/database/audit"
	"testing"
	"time"

	"entgo.io/ent"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/stretchr/testify/require"
)

func TestCreatedMixin(t *testing.T) {
	fields := (entdb.CreatedMixin{}).Fields()
	require.Len(t, fields, 4)
	for _, f := range fields {
		d := f.Descriptor()
		require.Contains(t, []string{"created_at", "created_by", "created_by_kind", "created_by_name"}, d.Name)
		require.True(t, d.Immutable)
		require.False(t, d.Optional)
	}
}

func TestGlobalAuditSkipsUnauditedMutation(t *testing.T) {
	h, err := entdb.NewAuditHook(entdb.AuditOptions{Actor: func(context.Context) (audit.Actor, error) {
		t.Fatal("unaudited schema must not require identity")
		return audit.Actor{}, nil
	}})
	require.NoError(t, err)
	m := &mutation{op: ent.OpCreate, fields: map[string]ent.Value{"name": "tea"}}
	value, err := h(ent.MutateFunc(func(_ context.Context, got ent.Mutation) (ent.Value, error) {
		require.Same(t, m, got)
		return "saved", nil
	})).Mutate(context.Background(), m)
	require.NoError(t, err)
	require.Equal(t, "saved", value)
	require.Equal(t, map[string]ent.Value{"name": "tea"}, m.fields)
}

func TestStandaloneIDHook(t *testing.T) {
	h, err := entdb.NewIDHook(mutationOptions().IDs)
	require.NoError(t, err)
	for _, op := range []ent.Op{ent.OpCreate, ent.OpUpdate, ent.OpUpdateOne, ent.OpDelete, ent.OpDeleteOne} {
		m := &mutation{op: op, id: 99}
		_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, nil })).Mutate(context.Background(), m)
		require.NoError(t, err)
		if op == ent.OpCreate {
			require.Equal(t, int64(42), m.id)
		} else {
			require.Equal(t, int64(99), m.id)
		}
		require.Empty(t, m.fields)
	}
}

func TestExplicitAuditProfiles(t *testing.T) {
	for _, profile := range []entdb.AuditProfile{entdb.AuditFull, entdb.AuditCreateOnly} {
		for _, op := range []ent.Op{ent.OpCreate, ent.OpUpdate, ent.OpUpdateOne, ent.OpDelete, ent.OpDeleteOne} {
			o := mutationOptions()
			calls := 0
			h, err := entdb.NewAuditHook(entdb.AuditOptions{Profile: profile, Actor: func(ctx context.Context) (audit.Actor, error) { calls++; return o.Actor(ctx) }, Now: o.Now})
			require.NoError(t, err)
			m := &mutation{op: op, id: 99, fields: map[string]ent.Value{"created_by": "forged"}}
			_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, nil })).Mutate(context.Background(), m)
			require.NoError(t, err)
			require.Equal(t, int64(99), m.id)
			if op == ent.OpCreate {
				require.Equal(t, "job", m.fields["created_by"])
				require.Equal(t, 1, calls)
			} else if op == ent.OpUpdate || op == ent.OpUpdateOne {
				require.NotContains(t, m.fields, "created_by")
				if profile == entdb.AuditCreateOnly {
					require.Zero(t, calls)
				} else {
					require.Equal(t, 1, calls)
				}
			} else {
				require.Zero(t, calls)
			}
			if profile == entdb.AuditCreateOnly {
				require.NotContains(t, m.fields, "updated_at")
			}
		}
	}
}

type hookClient struct{ hooks []ent.Hook }

func (c *hookClient) Use(h ...ent.Hook) { c.hooks = append(c.hooks, h...) }

func TestInstallHooksValidatesBeforeInstalling(t *testing.T) {
	h, err := entdb.NewIDHook(mutationOptions().IDs)
	require.NoError(t, err)
	a, b := &hookClient{}, &hookClient{}
	var missing *hookClient
	require.Error(t, entdb.InstallHooks([]ent.Hook{h}, a, missing))
	require.Empty(t, a.hooks)
	require.Error(t, entdb.InstallHooks([]ent.Hook{h, nil}, a))
	require.Empty(t, a.hooks)
	require.Error(t, entdb.InstallHooks([]ent.Hook{h}, nil))
	require.NoError(t, entdb.InstallHooks([]ent.Hook{h}, a, b))
	require.Len(t, a.hooks, 1)
	require.Len(t, b.hooks, 1)
}

func TestSplitHookInvalidOptions(t *testing.T) {
	for _, ids := range []entdb.IDGenerator{nil, mutationIDs(nil)} {
		h, err := entdb.NewIDHook(ids)
		require.Error(t, err)
		require.Nil(t, h)
	}
	for _, opts := range []entdb.AuditOptions{
		{Profile: entdb.AuditProfile(99), Actor: mutationOptions().Actor},
	} {
		h, err := entdb.NewAuditHook(opts)
		require.Error(t, err)
		require.Nil(t, h)
	}
}

func TestCreatedAuditNeverTouchesUpdateFields(t *testing.T) {
	o := mutationOptions()
	h, err := entdb.NewAuditHook(entdb.AuditOptions{Profile: entdb.AuditCreateOnly, Actor: o.Actor, Now: o.Now})
	require.NoError(t, err)
	m := &mutation{op: ent.OpCreate, fields: map[string]ent.Value{}, fail: "updated_at"}
	_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, nil })).Mutate(context.Background(), m)
	require.NoError(t, err)
	require.Len(t, m.fields, 4)
	require.Equal(t, o.Now().UTC().Truncate(time.Millisecond), m.fields["created_at"])
}

func TestCreatedAuditPreservesBusinessUpdates(t *testing.T) {
	h, err := entdb.NewAuditHook(entdb.AuditOptions{Profile: entdb.AuditCreateOnly, Actor: func(context.Context) (audit.Actor, error) {
		t.Fatal("update must not resolve actor")
		return audit.Actor{}, nil
	}})
	require.NoError(t, err)
	m := &mutation{op: ent.OpUpdate, fields: map[string]ent.Value{"name": "new", "created_by": "forged"}}
	next := h(ent.MutateFunc(func(_ context.Context, got ent.Mutation) (ent.Value, error) {
		require.Same(t, m, got)
		require.Equal(t, map[string]ent.Value{"name": "new"}, m.fields)
		return "saved", nil
	}))
	value, err := next.Mutate(context.Background(), m)
	require.NoError(t, err)
	require.Equal(t, "saved", value)

}

func TestSplitHooksRejectInvalidMutations(t *testing.T) {
	o := mutationOptions()
	idHook, err := entdb.NewIDHook(o.IDs)
	require.NoError(t, err)
	auditHook, err := entdb.NewAuditHook(entdb.AuditOptions{Profile: entdb.AuditCreateOnly, Actor: o.Actor})
	require.NoError(t, err)
	for _, h := range []ent.Hook{idHook, auditHook} {
		next := h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			t.Fatal("unexpected persistence")
			return nil, nil
		}))
		var missing *mutation
		for _, m := range []ent.Mutation{nil, missing, &mutation{op: ent.Op(0)}} {
			_, err := next.Mutate(context.Background(), m)
			require.Error(t, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := next.Mutate(ctx, &mutation{op: ent.OpCreate})
		require.ErrorIs(t, err, context.Canceled)
	}
}
