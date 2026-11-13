package category

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Repository defines only the capabilities this feature needs. Update must
// atomically load, invoke change, and write under a transaction/lock. Return an
// independent snapshot; never retain the callback pointer. Callback errors and
// observed context cancellation must discard all mutations. The callback must
// not escape its pointer. Find returns an independent snapshot too.
type Repository interface {
	Find(context.Context, ID) (Model, error)
	Update(context.Context, ID, func(*Model) error) (Model, error)
}

// Service checks business input and state for every caller.
type Service struct{ Repository Repository }

func input(actor string, id ID) error {
	if strings.TrimSpace(actor) == "" {
		return ErrUnauthenticated
	}
	if !id.Valid() {
		return ErrInvalid
	}
	return nil
}
func permitted(actor string, m Model) error {
	if m.Owner != actor {
		return ErrForbidden
	}
	return nil
}

// Find loads a category owned by the actor.
func (s Service) Find(ctx context.Context, actor string, id ID) (Model, error) {
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	if err := input(actor, id); err != nil {
		return Model{}, err
	}
	m, err := s.Repository.Find(ctx, id)
	if err != nil {
		return Model{}, err
	}
	if err := permitted(actor, m); err != nil {
		return Model{}, err
	}
	return m, nil
}

// Patch checks ownership and immutable state within the atomic update.
// Name and note limits are UTF-8 byte counts (100 and 1000), not rune counts.
func (s Service) Patch(ctx context.Context, actor string, cmd PatchCommand) (Model, error) {
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	if err := input(actor, cmd.ID); err != nil {
		return Model{}, err
	}
	if !cmd.Name.Set && !cmd.Note.Set && !cmd.Note.Clear {
		return Model{}, ErrInvalid
	}
	if cmd.Note.Set && cmd.Note.Clear {
		return Model{}, ErrInvalid
	}
	if cmd.Name.Set && ValidateName(cmd.Name.Value) != nil {
		return Model{}, ErrInvalid
	}
	if cmd.Note.Set && ValidateNote(cmd.Note.Value) != nil {
		return Model{}, ErrInvalid
	}
	return s.Repository.Update(ctx, cmd.ID, func(m *Model) error {
		if err := permitted(actor, *m); err != nil {
			return err
		}
		if m.System {
			return ErrImmutable
		}
		if cmd.Name.Set {
			m.Name = cmd.Name.Value
		}
		if cmd.Note.Set {
			m.Note = cmd.Note.Value
			m.HasNote = true
		}
		if cmd.Note.Clear {
			m.Note = ""
			m.HasNote = false
		}
		return nil
	})
}
func validText(value string) bool { return utf8.ValidString(value) && !strings.ContainsRune(value, 0) }

// ValidateName enforces a nonblank UTF-8 name of at most 100 bytes, without NUL.
func ValidateName(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 100 || !validText(value) {
		return ErrInvalid
	}
	return nil
}

// ValidateNote permits empty UTF-8 notes of at most 1000 bytes, without NUL.
func ValidateNote(value string) error {
	if len(value) > 1000 || !validText(value) {
		return ErrInvalid
	}
	return nil
}
