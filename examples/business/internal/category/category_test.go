package category

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCanonicalID(t *testing.T) {
	for _, s := range []string{"", "0", "-1", "+1", "01", " 1", "9223372036854775808"} {
		if _, err := ParseID(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseID(%q): %v", s, err)
		}
	}
	for _, s := range []string{"1", "9223372036854775807"} {
		id, err := ParseID(s)
		if err != nil || id.String() != s || !id.Valid() {
			t.Errorf("ParseID(%q): %v, %v", s, id, err)
		}
	}
	if _, err := NewID(0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if (ID{}).Valid() {
		t.Fatal("zero ID valid")
	}
}

// memoryRepository implements the example contract, not a production store.
// Holding the lock covers load/check/write; callbacks see only a detached copy.
type memoryRepository struct {
	mu      sync.Mutex
	model   Model
	seen    context.Context
	failure error
}

func (r *memoryRepository) Find(ctx context.Context, id ID) (Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = ctx
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	if r.failure != nil {
		return Model{}, r.failure
	}
	if r.model.ID != id {
		return Model{}, ErrNotFound
	}
	return r.model, nil
}
func (r *memoryRepository) Update(ctx context.Context, id ID, change func(*Model) error) (Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = ctx
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	if r.failure != nil {
		return Model{}, r.failure
	}
	if r.model.ID != id {
		return Model{}, ErrNotFound
	}
	next := r.model
	if err := change(&next); err != nil {
		return Model{}, err
	}
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	r.model = next
	return next, nil
}
func fixture() (*memoryRepository, Service, ID) {
	id, _ := NewID(1)
	r := &memoryRepository{model: Model{ID: id, Owner: "alice", Name: "old", Note: "kept", HasNote: true}}
	return r, Service{Repository: r}, id
}
func TestPatchNoteStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  NoteChange
		note    string
		present bool
	}{
		{"omitted", NoteChange{}, "kept", true}, {"clear", NoteChange{Clear: true}, "", false}, {"empty value", NoteChange{Set: true, Value: ""}, "", true}, {"value", NoteChange{Set: true, Value: "new"}, "new", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, id := fixture()
			out, err := s.Patch(context.Background(), "alice", PatchCommand{ID: id, Name: NameChange{Set: true, Value: "updated"}, Note: tc.change})
			if err != nil || out.Note != tc.note || out.HasNote != tc.present || r.model.Name != "updated" {
				t.Fatalf("%+v, %v", out, err)
			}
		})
	}
}
func TestBusinessGuards(t *testing.T) {
	for _, tc := range []struct {
		name, actor string
		command     PatchCommand
		system      bool
		want        error
	}{
		{name: "missing actor", want: ErrUnauthenticated}, {name: "wrong owner", actor: "bob", want: ErrForbidden}, {name: "immutable", actor: "alice", system: true, want: ErrImmutable},
		{name: "invalid ID", actor: "alice", command: PatchCommand{Name: NameChange{Set: true, Value: "ok"}}, want: ErrInvalid},
		{name: "invalid name", actor: "alice", command: PatchCommand{ID: ID{value: 1}, Name: NameChange{Set: true, Value: " "}}, want: ErrInvalid},
		{name: "conflicting change", actor: "alice", command: PatchCommand{ID: ID{value: 1}, Note: NoteChange{Set: true, Clear: true}}, want: ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, id := fixture()
			r.model.System = tc.system
			before := r.model
			cmd := tc.command
			if tc.name != "invalid ID" && cmd.ID == (ID{}) {
				cmd = PatchCommand{ID: id, Name: NameChange{Set: true, Value: "new"}}
			}
			_, err := s.Patch(context.Background(), tc.actor, cmd)
			if !errors.Is(err, tc.want) || r.model != before {
				t.Fatalf("%v, model changed=%v", err, r.model != before)
			}
		})
	}
}
func TestRepositoryRollbackAndNoRetainedPointer(t *testing.T) {
	r, _, id := fixture()
	before := r.model
	rejection := errors.New("reject")
	var retained *Model
	_, err := r.Update(context.Background(), id, func(m *Model) error { m.Name = "bad"; return rejection })
	if !errors.Is(err, rejection) || r.model != before {
		t.Fatal("rejection committed")
	}
	_, err = r.Update(context.Background(), id, func(m *Model) error { m.Name = "good"; retained = m; return nil })
	if err != nil {
		t.Fatal(err)
	}
	retained.Name = "later"
	if r.model.Name != "good" {
		t.Fatal("retained pointer changed store")
	}
}
func TestContextAndCause(t *testing.T) {
	r, s, id := fixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.Find(ctx, "alice", id); err != nil || r.seen != ctx {
		t.Fatal("context not forwarded", err)
	}
	cause := errors.New("storage private")
	r.failure = fmt.Errorf("wrapped: %w", cause)
	if _, err := s.Patch(ctx, "alice", PatchCommand{ID: id, Note: NoteChange{Clear: true}}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Find(ctx, "alice", id); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestUTF8ByteLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"boundary", strings.Repeat("界", 33) + "a", true},
		{"over boundary", strings.Repeat("界", 34), false},
		{"invalid UTF8", string([]byte{0xff}), false},
		{"NUL", "a\x00b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, id := fixture()
			before := r.model
			_, err := s.Patch(context.Background(), "alice", PatchCommand{ID: id, Name: NameChange{Set: true, Value: tc.value}})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrInvalid) || r.model != before {
				t.Fatal("invalid input changed model", err)
			}
		})
	}
}
