package validation

import (
	"errors"
	"testing"
)

func TestIssueBudgetIsInputLimit(t *testing.T) {
	c := NewContext(nil, 1)
	c.Add("", "invalid", "invalid input")
	c.Add("", "invalid", "invalid input")
	if errors.Is(c.Err(), ErrSchema) || !errors.Is(c.Err(), ErrLimit) || !errors.Is(c.Err(), ErrInvalid) {
		t.Fatalf("input issue budget must not become schema failure: %v", c.Err())
	}
}

func TestScopeCannotRestoreExhaustedBudget(t *testing.T) {
	c := NewContext(nil, 1)
	c.Add("", "invalid", "invalid input")
	child := c.Scope("Child")
	child.Add("", "invalid", "invalid input")
	if child.Err() == nil || errors.Is(child.Err(), ErrSchema) {
		t.Fatalf("exhausted scoped budget must remain an input limit: %v", child.Err())
	}
	c.Merge(child.Err())
	if c.Err() == nil || errors.Is(c.Err(), ErrSchema) {
		t.Fatalf("merging scoped limit must preserve input classification: %v", c.Err())
	}
}
