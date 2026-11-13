package validation_test

import (
	"testing"

	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

func TestCompleteIndependentIssues(t *testing.T) {
	c := validation.NewContext(map[string]validation.FieldState{
		"Name":  {Path: validation.Path{"name"}, Present: true, Decoded: true},
		"Price": {Path: validation.Path{"price"}, Present: true, Decoded: true},
	}, 10)
	c.Add("Name", "required", "名称不能为空")
	c.Add("Price", "min", "价格不能为负数")
	require.ErrorIs(t, c.Err(), validation.ErrInvalid)
	issues := c.Issues()
	require.Len(t, issues, 2)
	issues[0].Path[0] = "changed"
	require.Equal(t, "name", c.Issues()[0].Path[0])
	require.False(t, c.Valid("Name"))
	require.True(t, c.Decoded("Name"))
}

func TestInvalidDeclarationAndBudgetNeverReturnPartialSuccess(t *testing.T) {
	c := validation.NewContext(map[string]validation.FieldState{"Name": {Path: validation.Path{"name"}, Decoded: true}}, 1)
	c.Add("Name", "min", "too short")
	c.Add("Name", "max", "too long")
	require.ErrorIs(t, c.Err(), validation.ErrLimit)
	other := validation.NewContext(nil, 10)
	other.Add("Unknown", "required", "not safe")
	require.ErrorIs(t, other.Err(), validation.ErrSchema)
}

func TestContextScopeAtExactBudgetAndInvalidPath(t *testing.T) {
	c := validation.NewContext(map[string]validation.FieldState{"Name": {Path: validation.Path{"name"}, Decoded: true}, "Child.Name": {Path: validation.Path{"child", "name"}, Decoded: true}}, 1)
	c.Add("Name", "required", "name required")
	sub := c.Scope("Child")
	require.NoError(t, sub.Err())
	sub.Add("Name", "required", "child required")
	require.ErrorIs(t, sub.Err(), validation.ErrLimit)
	bad := validation.NewContext(map[string]validation.FieldState{"Name": {Path: validation.Path{[]string{"unsafe"}}}}, 1)
	require.NotPanics(t, func() { bad.Add("Name", "required", "name required") })
	require.ErrorIs(t, bad.Err(), validation.ErrSchema)
}
