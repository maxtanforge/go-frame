package categoryhttp

import "github.com/maxtanforge/go-frame/examples/business/internal/category"

// CategoryVO is an explicit whitelist. Owner, parent and system state stay private.
type CategoryVO struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Note *string `json:"note"`
}

func public(m category.Model) *CategoryVO {
	out := &CategoryVO{ID: m.ID.String(), Name: m.Name}
	if m.HasNote {
		note := m.Note
		out.Note = &note
	}
	return out
}

// DeleteVO reports successful deletion.
type DeleteVO struct {
	Deleted bool `json:"deleted"`
}
