package validation

import (
	"encoding/json"
	"errors"
	"strings"
)

// FieldState describes one concrete field, independent of its current zero value.
type FieldState struct {
	Path                            Path
	Label                           string
	Present, Null, Decoded, Invalid bool
}

// Context is a per-validation collector, not context.Context. Do not share it
// across requests or goroutines. Field keys are declared Go field paths.
type Context struct {
	byPath  map[string][]string
	states  map[string]FieldState
	issues  []Issue
	limit   int
	failure error
}

// NewContext builds a collector over decoded field states; limit <= 0 uses 4096.
func NewContext(states map[string]FieldState, limit int) *Context {
	if limit <= 0 {
		limit = 4096
	}
	c := &Context{states: make(map[string]FieldState, len(states)), limit: limit, byPath: make(map[string][]string, len(states))}
	for key, state := range states {
		if !ValidIssue(Issue{Path: state.Path, Code: "validation", Message: state.Label}) {
			c.failure = ErrSchema
			continue
		}
		state.Path = append(Path{}, state.Path...)
		c.states[key] = state
		k := pathKey(state.Path)
		c.byPath[k] = append(c.byPath[k], key)
	}
	return c
}

// Present reports whether the request supplied the field.
func (c *Context) Present(field string) bool { s, ok := c.states[field]; return ok && s.Present }

// Null reports whether the field arrived as an explicit null.
func (c *Context) Null(field string) bool { s, ok := c.states[field]; return ok && s.Null }

// Decoded reports whether the value converted into the target type.
func (c *Context) Decoded(field string) bool { s, ok := c.states[field]; return ok && s.Decoded }

// Invalid reports whether any recorded issue marks the field invalid.
func (c *Context) Invalid(field string) bool { s, ok := c.states[field]; return ok && s.Invalid }

// Valid requires a decoded field with no recorded issue; use it to gate
// value rules that must only run on successfully converted input.
func (c *Context) Valid(field string) bool {
	s, ok := c.states[field]
	return ok && s.Decoded && !s.Invalid
}

// Add records a field issue and marks the field invalid; empty field records
// a request-wide issue. Code and message follow the Issue contract.
func (c *Context) Add(field, code, message string) {
	if field == "" {
		c.Record(Issue{Code: code, Message: message})
		return
	}
	s, ok := c.states[field]
	if !ok {
		c.failure = ErrSchema
		return
	}
	c.Record(Issue{Path: s.Path, Code: code, Message: message})
	s.Invalid = true
	c.states[field] = s
}

// Record appends a safe already-resolved issue (for binding and validation adapters).
func (c *Context) Record(issue Issue) {
	if c.failure != nil {
		return
	}
	if !ValidIssue(issue) {
		c.failure = ErrSchema
		return
	}
	if len(c.issues) >= c.limit {
		c.failure = ErrLimit
		return
	}
	c.issues = append(c.issues, cloneIssue(issue))
	for _, name := range c.byPath[pathKey(issue.Path)] {
		state := c.states[name]
		state.Invalid = true
		c.states[name] = state
	}
}

func pathKey(path Path) string {
	if len(path) == 0 {
		return ""
	}
	data, _ := json.Marshal(path)
	return string(data)
}

// Scope gives a nested custom validator relative Go field names. Its result
// must be merged into the parent collector with Merge after the callback.
func (c *Context) Scope(prefix string) *Context {
	states := map[string]FieldState{}
	for name, state := range c.states {
		if strings.HasPrefix(name, prefix+".") {
			states[strings.TrimPrefix(name, prefix+".")] = state
		}
	}
	child := NewContext(states, c.limit-len(c.issues))
	// NewContext treats zero as a default; a scope must keep the exact
	// remaining budget, including zero, instead of restoring 4096 issues.
	child.limit = c.limit - len(c.issues)
	return child
}

// Merge adopts an unwrapped *Errors collection issue by issue; any other
// error (including wrapped collections) becomes the terminal failure and keeps
// its text private, so upper layers decide public semantics.
func (c *Context) Merge(err error) { //nolint:errorlint // 只合并未包装集合;包装错误按 failure 交给上层
	if err == nil {
		return
	}
	if errors.Is(err, ErrSchema) {
		c.failure = err
		return
	}
	all, ok := err.(*Errors) //nolint:errorlint // 只合并未包装集合;包装错误按 failure 交给上层
	if !ok {
		c.failure = err
		return
	}
	for _, issue := range all.Issues() {
		c.Record(issue)
	}
}

// Issues returns recorded issues, or nil once the collector failed.
func (c *Context) Issues() []Issue {
	if c.failure != nil {
		return nil
	}
	return NewErrors(c.issues...).Issues()
}

// Err returns the terminal failure, a collection of recorded issues, or nil.
func (c *Context) Err() error {
	if c.failure != nil {
		return c.failure
	}
	if len(c.issues) == 0 {
		return nil
	}
	return NewErrors(c.issues...)
}

// Check attaches field-independent value rules only to successfully decoded values.
func Check[T any](c *Context, field string, value T, rule func(T) error) {
	if _, ok := c.states[field]; !ok || rule == nil {
		c.failure = ErrSchema
		return
	}
	if !c.Decoded(field) || c.Null(field) {
		return
	}
	err := rule(value)
	if err == nil {
		return
	}
	if errors.Is(err, ErrSchema) {
		c.failure = err
		return
	}
	issues, ok := err.(*Errors) //nolint:errorlint // 只采纳未包装集合,与 Merge 同契约
	if ok {
		for _, issue := range issues.Issues() {
			s := c.states[field]
			issue.Path = append(append(Path{}, s.Path...), issue.Path...)
			c.Record(issue)
		}
		s := c.states[field]
		s.Invalid = true
		c.states[field] = s
		return
	}
	c.failure = err // arbitrary/internal errors are never promoted to public text
}
