package category

// Model is a business snapshot. Persistence and transport select their own fields.
// Note/HasNote distinguish an empty note from no note without mutable pointers.
type Model struct {
	ID, ParentID      ID
	Owner, Name, Note string
	HasNote, System   bool
}

// NameChange keeps the name unless Set is true.
type NameChange struct {
	Set   bool
	Value string
}

// NoteChange keeps the note in its zero value; Set stores Value and Clear removes it.
// Set and Clear together are invalid, including for non-HTTP callers.
type NoteChange struct {
	Set, Clear bool
	Value      string
}

// PatchCommand exposes only mutable fields and the lookup ID.
type PatchCommand struct {
	ID   ID
	Name NameChange
	Note NoteChange
}
