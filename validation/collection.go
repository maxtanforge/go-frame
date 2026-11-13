package validation

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Path contains declared field names and non-negative array indices only.
type Path []any

// Issue explicitly marks a developer-controlled diagnostic as public.
// Never put raw input or an internal error's Error() text in Message.
type Issue struct {
	Path    Path   `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Errors owns an ordered snapshot; Error deliberately excludes public messages.
type Errors struct {
	issues  []Issue
	invalid bool
}

// NewErrors snapshots valid issues; any invalid issue poisons the collection
// into the schema-error state.
func NewErrors(issues ...Issue) *Errors {
	e := &Errors{}
	for _, issue := range issues {
		if !ValidIssue(issue) {
			e.invalid = true
			return e
		}
		e.issues = append(e.issues, cloneIssue(issue))
	}
	return e
}

func (e *Errors) Error() string {
	if e != nil && e.invalid {
		return ErrSchema.Error()
	}
	return ErrInvalid.Error()
}
func (e *Errors) Unwrap() error {
	if e != nil && e.invalid {
		return ErrSchema
	}
	return ErrInvalid
}

// Issues returns an independent copy; nil for a nil or poisoned collection.
func (e *Errors) Issues() []Issue {
	if e == nil || e.invalid {
		return nil
	}
	result := make([]Issue, len(e.issues))
	for i, issue := range e.issues {
		result[i] = cloneIssue(issue)
	}
	return result
}
func cloneIssue(issue Issue) Issue {
	issue.Path = append(Path{}, issue.Path...)
	return issue
}

// ValidIssue checks structure, not whether a developer's message is appropriate
// to disclose. That remains the responsibility of the caller.
func ValidIssue(issue Issue) bool {
	if len(issue.Path) > 64 || issue.Code == "" || !validName(issue.Code) || len(issue.Message) > 1024 || !utf8.ValidString(issue.Message) {
		return false
	}
	for _, r := range issue.Message {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range issue.Path {
		switch v := segment.(type) {
		case string:
			if v == "" || !validName(v) {
				return false
			}
		case int:
			if v < 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// FirstMessage only reads an explicit validation collection, not arbitrary
// wrapped internal causes. Response adapters must apply error precedence first.
func (e *Errors) FirstMessage() string {
	if e != nil && !e.invalid && len(e.issues) > 0 && strings.TrimSpace(e.issues[0].Message) != "" {
		return e.issues[0].Message
	}
	return "请求参数不正确"
}
