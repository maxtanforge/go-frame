package entdb_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"github.com/maxtanforge/go-frame/database/audit"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/stretchr/testify/require"
)

type mutationIDs func(context.Context) (int64, error)

func (f mutationIDs) NextContext(ctx context.Context) (int64, error) { return f(ctx) }

type mutation struct {
	ent.Mutation
	op     ent.Op
	id     int64
	fields map[string]ent.Value
	fail   string
}

func (m *mutation) Op() ent.Op     { return m.op }
func (m *mutation) SetID(id int64) { m.id = id }
func (m *mutation) SetField(name string, v ent.Value) error {
	if name == m.fail {
		return errMutationField
	}
	want := reflect.TypeOf("")
	if name == "created_at" || name == "updated_at" {
		want = reflect.TypeOf(time.Time{})
	}
	if reflect.TypeOf(v) != want {
		return errMutationField
	}
	m.fields[name] = v
	return nil
}
func (m *mutation) ResetField(name string) error {
	if name == m.fail {
		return errMutationField
	}
	delete(m.fields, name)
	return nil
}

var errMutationField = errors.New("field unavailable")

func mutationOptions() entdb.MutationOptions {
	return entdb.MutationOptions{
		IDs: mutationIDs(func(context.Context) (int64, error) { return 42, nil }),
		Actor: func(context.Context) (audit.Actor, error) {
			return audit.Actor{Kind: audit.System, ID: "job", Name: "Scheduled job"}, nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.FixedZone("east", 3600)) },
	}
}
func TestMixinFields(t *testing.T) {
	fields := (entdb.IDMixin{}).Fields()
	require.Len(t, fields, 1)
	d := fields[0].Descriptor()
	require.Equal(t, field.TypeInt64, d.Info.Type)
	require.True(t, d.Immutable)
	require.Equal(t, `json:"id,string"`, d.Tag)
	require.NotEmpty(t, d.Validators)
	require.NoError(t, d.Validators[0].(func(int64) error)(1))
	require.Error(t, d.Validators[0].(func(int64) error)(0))
	require.False(t, *d.Annotations[0].(entsql.Annotation).Incremental)
	fields = (entdb.AuditMixin{}).Fields()
	require.Len(t, fields, 8)
	for _, f := range fields {
		d := f.Descriptor()
		require.Equal(t, len(d.Name) > 8 && d.Name[:8] == "created_", d.Immutable)
		require.False(t, d.Optional)
	}
}
func TestMutationHookWritesSystemFields(t *testing.T) {
	for _, op := range []ent.Op{ent.OpCreate, ent.OpUpdate, ent.OpUpdateOne} {
		t.Run(op.String(), func(t *testing.T) {
			opts := mutationOptions()
			idsCalled := false
			opts.IDs = mutationIDs(func(context.Context) (int64, error) { idsCalled = true; return 42, nil })
			hook, err := entdb.NewMutationHook(opts)
			require.NoError(t, err)
			m := &mutation{op: op, id: 99, fields: map[string]ent.Value{"created_by": "forged", "updated_by": "forged", "created_at": time.Time{}, "updated_at": time.Time{}}}
			called := false
			result, err := hook(ent.MutateFunc(func(_ context.Context, got ent.Mutation) (ent.Value, error) {
				called = true
				require.Same(t, m, got)
				return "saved", nil
			})).Mutate(context.Background(), m)
			require.NoError(t, err)
			require.True(t, called)
			require.Equal(t, "saved", result)
			require.Equal(t, time.Date(2026, 9, 28, 11, 0, 0, 123000000, time.UTC), m.fields["updated_at"])
			require.Equal(t, "job", m.fields["updated_by"])
			require.Equal(t, "system", m.fields["updated_by_kind"])
			require.Equal(t, "Scheduled job", m.fields["updated_by_name"])
			require.Equal(t, op == ent.OpCreate, idsCalled)
			if op == ent.OpCreate {
				require.Equal(t, int64(42), m.id)
				require.Equal(t, m.fields["updated_at"], m.fields["created_at"])
				require.Equal(t, "job", m.fields["created_by"])
				require.Equal(t, "system", m.fields["created_by_kind"])
				require.Equal(t, "Scheduled job", m.fields["created_by_name"])
			} else {
				require.Equal(t, int64(99), m.id)
				require.NotContains(t, m.fields, "created_by")
				require.NotContains(t, m.fields, "created_at")
			}
		})
	}
}
func TestMutationHookRejectsInvalidOptions(t *testing.T) {
	for _, change := range []func(*entdb.MutationOptions){func(o *entdb.MutationOptions) { o.IDs = nil }, func(o *entdb.MutationOptions) { var ids mutationIDs; o.IDs = ids }} {
		o := mutationOptions()
		change(&o)
		h, err := entdb.NewMutationHook(o)
		require.Error(t, err)
		require.Nil(t, h)
	}
}
func TestMutationHookFailures(t *testing.T) {
	failure := errors.New("dependency")
	for _, tc := range []struct {
		name   string
		change func(*entdb.MutationOptions)
		want   error
	}{
		{"id", func(o *entdb.MutationOptions) {
			o.IDs = mutationIDs(func(context.Context) (int64, error) { return 0, failure })
		}, failure},
		{"nonpositive id", func(o *entdb.MutationOptions) {
			o.IDs = mutationIDs(func(context.Context) (int64, error) { return 0, nil })
		}, nil},
		{"actor", func(o *entdb.MutationOptions) {
			o.Actor = func(context.Context) (audit.Actor, error) { return audit.Actor{}, failure }
		}, failure},
		{"invalid actor", func(o *entdb.MutationOptions) {
			o.Actor = func(context.Context) (audit.Actor, error) { return audit.Actor{}, nil }
		}, audit.ErrInvalid},
		{"invalid time", func(o *entdb.MutationOptions) { o.Now = func() time.Time { return time.Time{} } }, audit.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := mutationOptions()
			tc.change(&o)
			h, err := entdb.NewMutationHook(o)
			require.NoError(t, err)
			_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
				t.Fatal("unexpected persistence")
				return nil, nil
			})).Mutate(context.Background(), &mutation{op: ent.OpCreate, fields: map[string]ent.Value{}})
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}
func TestMutationHookCancellationAndDelete(t *testing.T) {
	o := mutationOptions()
	o.Actor = func(context.Context) (audit.Actor, error) {
		t.Fatal("unexpected actor resolution")
		return audit.Actor{}, nil
	}
	o.IDs = mutationIDs(func(context.Context) (int64, error) { t.Fatal("unexpected ID generation"); return 0, nil })
	h, err := entdb.NewMutationHook(o)
	require.NoError(t, err)
	calls := 0
	next := h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { calls++; return 1, nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = next.Mutate(ctx, &mutation{op: ent.OpCreate})
	require.ErrorIs(t, err, context.Canceled)
	for _, op := range []ent.Op{ent.OpDelete, ent.OpDeleteOne} {
		_, err = next.Mutate(context.Background(), &mutation{op: op})
		require.NoError(t, err)
	}
	require.Equal(t, 2, calls)
}
func TestMutationHookPropagatesFieldAndPersistenceErrors(t *testing.T) {
	for _, op := range []ent.Op{ent.OpCreate, ent.OpUpdate, ent.OpUpdateOne} {
		for _, name := range []string{"created_at", "created_by", "created_by_kind", "updated_at", "updated_by", "updated_by_kind"} {
			h, err := entdb.NewMutationHook(mutationOptions())
			require.NoError(t, err)
			_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
				t.Fatal("unexpected persistence")
				return nil, nil
			})).Mutate(context.Background(), &mutation{op: op, fields: map[string]ent.Value{}, fail: name})
			require.ErrorIs(t, err, errMutationField)
		}
	}
	o := mutationOptions()
	o.Now = nil
	h, err := entdb.NewMutationHook(o)
	require.NoError(t, err)
	_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errMutationField })).Mutate(context.Background(), &mutation{op: ent.OpCreate, fields: map[string]ent.Value{}})
	require.ErrorIs(t, err, errMutationField)
}

func TestMutationHookExplicitActors(t *testing.T) {
	for _, actor := range []audit.Actor{{Kind: audit.Anonymous}, {Kind: audit.Subject, ID: "user:123"}} {
		t.Run(string(actor.Kind), func(t *testing.T) {
			o := mutationOptions()
			o.Actor = func(context.Context) (audit.Actor, error) { return actor, nil }
			h, err := entdb.NewMutationHook(o)
			require.NoError(t, err)
			m := &mutation{op: ent.OpCreate, fields: map[string]ent.Value{}}
			_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, nil })).Mutate(context.Background(), m)
			require.NoError(t, err)
			require.Equal(t, actor.ID, m.fields["created_by"])
			require.Equal(t, string(actor.Kind), m.fields["created_by_kind"])
		})
	}
}

// An unrelated generated schema has no int64 SetID method.
type unrelatedMutation struct{ ent.Mutation }

func (*unrelatedMutation) Op() ent.Op { return ent.OpCreate }

func TestMutationHookRejectsMissingIDAndNilMutation(t *testing.T) {
	h, err := entdb.NewMutationHook(mutationOptions())
	require.NoError(t, err)
	next := h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
		t.Fatal("unexpected persistence")
		return nil, nil
	}))
	var typedNil *mutation
	for _, m := range []ent.Mutation{nil, typedNil, &unrelatedMutation{}, &mutation{op: ent.Op(0)}} {
		_, err := next.Mutate(context.Background(), m)
		require.Error(t, err)
	}
}

func TestMutationHookChecksCancellationAfterDependencies(t *testing.T) {
	for _, dependency := range []string{"actor", "id"} {
		t.Run(dependency, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := mutationOptions()
			o.Actor = func(got context.Context) (audit.Actor, error) {
				require.Same(t, ctx, got)
				if dependency == "actor" {
					cancel()
				}
				return audit.Actor{Kind: audit.System, ID: "job"}, nil
			}
			o.IDs = mutationIDs(func(got context.Context) (int64, error) {
				require.Same(t, ctx, got)
				if dependency == "id" {
					cancel()
				}
				return 42, nil
			})
			h, err := entdb.NewMutationHook(o)
			require.NoError(t, err)
			_, err = h(ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
				t.Fatal("unexpected persistence")
				return nil, nil
			})).Mutate(ctx, &mutation{op: ent.OpCreate, fields: map[string]ent.Value{}})
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}
