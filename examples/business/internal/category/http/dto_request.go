package categoryhttp

import (
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/validation"
)

type detailPath struct{ ID string }

// Validate checks transport syntax only. Business permissions and state belong
// to Service; callback mapping does not repeat generic DTO validation.
func (p detailPath) Validate(c *validation.Context) {
	if c.Valid("ID") {
		if _, err := category.ParseID(p.ID); err != nil {
			c.Add("ID", "canonical_id", "category ID must be a canonical positive integer")
		}
	}
}

type patchDTO struct {
	Name optional.Field[string] `json:"name"`
	Note optional.Field[string] `json:"note" nullable:"true"`
}

func (p patchDTO) command(id category.ID) category.PatchCommand {
	return category.PatchCommand{ID: id, Name: category.NameChange{Set: p.Name.Present, Value: p.Name.Value}, Note: category.NoteChange{Set: p.Note.Present && !p.Note.Null, Clear: p.Note.Present && p.Note.Null, Value: p.Note.Value}}
}

type createPath struct {
	ParentID string `path:"bookId" v:"present"`
}

func (p createPath) Validate(c *validation.Context) {
	if c.Valid("ParentID") {
		if _, err := category.ParseID(p.ParentID); err != nil {
			c.Add("ParentID", "canonical_id", "book ID must be a canonical positive integer")
		}
	}
}

type createDTO struct {
	Name string `json:"name" v:"present"`
}

type listDTO struct {
	Current int64 `q:"current" d:"1" v:"gte=1"`
	Size    int64 `q:"size" d:"20" v:"gte=1,lte=100"`
}

func (q listDTO) Validate(c *validation.Context) {
	if c.Valid("Current") && c.Valid("Size") && q.Size > 0 && q.Current-1 > 1_000_000/q.Size {
		c.Add("Current", "offset", "category offset exceeds limit")
	}
}
