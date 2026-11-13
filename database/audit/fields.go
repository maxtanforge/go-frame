package audit

import "time"

// Fields is composable metadata, not an audit event history. Exported fields
// permit explicit persistence/import; callers must validate reconstructed data.
type Fields struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy Actor     `json:"created_by"`
	UpdatedBy Actor     `json:"updated_by"`
}

// New captures one caller-supplied instant for both timestamps. UTC milliseconds
// and the year range 1000..9999 permit the documented MySQL/PG example schemas.
func New(actor Actor, at time.Time) (Fields, error) {
	if actor.Validate() != nil {
		return Fields{}, ErrInvalid
	}
	t, err := normalize(at)
	if err != nil {
		return Fields{}, err
	}
	return Fields{CreatedAt: t, UpdatedAt: t, CreatedBy: actor, UpdatedBy: actor}, nil
}

// WithUpdate preserves creation metadata and the original value. Equal update
// times are allowed; backwards clocks are rejected instead of silently clamped.
// This does not prevent lost updates: the database still owns concurrency control.
func (f Fields) WithUpdate(actor Actor, at time.Time) (Fields, error) {
	if f.Validate() != nil || actor.Validate() != nil {
		return Fields{}, ErrInvalid
	}
	t, err := normalize(at)
	if err != nil {
		return Fields{}, err
	}
	if t.Before(f.UpdatedAt) {
		return Fields{}, ErrInvalid
	}
	f.UpdatedAt = t
	f.UpdatedBy = actor
	return f, nil
}

// Validate requires canonical UTC millisecond timestamps and an ordered history.
// Importers explicitly normalize their source instants before constructing Fields.
func (f Fields) Validate() error {
	if f.CreatedBy.Validate() != nil || f.UpdatedBy.Validate() != nil {
		return ErrInvalid
	}
	for _, t := range []time.Time{f.CreatedAt, f.UpdatedAt} {
		n, err := normalize(t)
		if err != nil || t.Location() != time.UTC || t != n {
			return ErrInvalid
		}
	}
	if f.UpdatedAt.Before(f.CreatedAt) {
		return ErrInvalid
	}
	return nil
}

func normalize(at time.Time) (time.Time, error) {
	if at.IsZero() {
		return time.Time{}, ErrInvalid
	}
	t := at.UTC().Truncate(time.Millisecond)
	if t.Year() < 1000 || t.Year() > 9999 {
		return time.Time{}, ErrInvalid
	}
	return t, nil
}
