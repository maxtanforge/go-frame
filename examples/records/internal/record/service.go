// Package record owns the feature's business rules and persistence contracts.
package record

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maxtanforge/go-frame/database/audit"
	"github.com/maxtanforge/go-frame/pagination"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrInvalid  = errors.New("invalid record")
)

type Record struct {
	audit.Fields
	ID    int64  `json:"id,string"`
	Title string `json:"title"`
}

// Repository persists records. These interfaces belong to the service;
// neither SQL nor HTTP leaks into them.
type Repository interface {
	Find(context.Context, int64) (Record, error)
	List(context.Context, pagination.Window, []pagination.Sort) ([]Record, error)
}

// List owns this feature's pagination policy; callers must supply explicit values.
func (s Service) List(ctx context.Context, current, size int64, sorts []pagination.Sort) (pagination.Slice[Record], error) {
	if err := ctx.Err(); err != nil {
		return pagination.Slice[Record]{}, err
	}
	w, err := pagination.NewWindow(current, size, pagination.DefaultLimits())
	if err != nil {
		return pagination.Slice[Record]{}, ErrInvalid
	}
	if err := pagination.ValidateSort(sorts, []string{"id", "title"}); err != nil {
		return pagination.Slice[Record]{}, ErrInvalid
	}
	rows, err := s.Repository.List(ctx, w, sorts)
	if err != nil {
		return pagination.Slice[Record]{}, err
	}
	return pagination.NewSlice(rows, w)
}

type Writer interface {
	Insert(context.Context, Record) error
	AuditCreated(context.Context, Record) error
}
type Transactions interface {
	WithinTransaction(context.Context, func(Writer) error) error
}
type IDGenerator interface {
	NextContext(context.Context) (int64, error)
}

type Service struct {
	IDs          IDGenerator
	Repository   Repository
	Transactions Transactions
	Now          func() time.Time
}

func (s Service) Create(ctx context.Context, title string, actor audit.Actor) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if err := actor.Validate(); err != nil {
		return Record{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsRune(title, '\x00') || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 200 {
		return Record{}, ErrInvalid
	}
	if s.IDs == nil {
		return Record{}, errors.New("records: ID generator is required")
	}
	nextID, err := s.IDs.NextContext(ctx)
	if err != nil {
		return Record{}, err
	}
	if nextID <= 0 {
		return Record{}, errors.New("records: generator returned an invalid ID")
	}
	r := Record{ID: nextID, Title: title}
	err = s.Transactions.WithinTransaction(ctx, func(w Writer) error {
		now := s.Now
		if now == nil {
			now = time.Now
		}
		fields, err := audit.New(actor, now())
		if err != nil {
			return err
		}
		r.Fields = fields
		if err := w.Insert(ctx, r); err != nil {
			return err
		}
		return w.AuditCreated(ctx, r)
	})
	if err != nil {
		return Record{}, err
	}
	return r, nil
}

func (s Service) Find(ctx context.Context, id int64) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if id <= 0 {
		return Record{}, ErrInvalid
	}
	return s.Repository.Find(ctx, id)
}
