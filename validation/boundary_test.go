package validation_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

func TestIssueDisclosureBoundaries(t *testing.T) {
	valid := validation.Issue{Path: validation.Path{"items", 0, "_name-1"}, Code: "required", Message: "名称不能为空"}
	require.True(t, validation.ValidIssue(valid))
	deepPath := make(validation.Path, 65)
	for i := range deepPath {
		deepPath[i] = 0
	}
	require.True(t, validation.ValidIssue(validation.Issue{Path: deepPath[:64], Code: "invalid"}))
	for _, tc := range []struct {
		name  string
		issue validation.Issue
	}{
		{"empty code", validation.Issue{}},
		{"invalid code", validation.Issue{Code: "1bad"}},
		{"empty segment", validation.Issue{Path: validation.Path{""}, Code: "invalid"}},
		{"negative index", validation.Issue{Path: validation.Path{-1}, Code: "invalid"}},
		{"unsupported index", validation.Issue{Path: validation.Path{int64(1)}, Code: "invalid"}},
		{"deep path", validation.Issue{Path: deepPath, Code: "invalid"}},
		{"long message", validation.Issue{Code: "invalid", Message: strings.Repeat("a", 1025)}},
		{"invalid utf8", validation.Issue{Code: "invalid", Message: "\xff"}},
		{"control character", validation.Issue{Code: "invalid", Message: "private\nvalue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, validation.ValidIssue(tc.issue))
			all := validation.NewErrors(valid, tc.issue)
			require.ErrorIs(t, all, validation.ErrSchema)
			require.NotErrorIs(t, all, validation.ErrInvalid)
			require.Nil(t, all.Issues(), "poisoned collection must not disclose partial issues")
			require.Equal(t, validation.ErrSchema.Error(), all.Error())
			require.Equal(t, "请求参数不正确", all.FirstMessage())
		})
	}
	valid.Message = strings.Repeat("a", 1024)
	valid.Path = validation.Path{strings.Repeat("a", 64)}
	require.True(t, validation.ValidIssue(valid))
}

func TestIssueSnapshotsAndPrivateErrorText(t *testing.T) {
	issue := validation.Issue{Path: validation.Path{"name"}, Code: "required", Message: "public explanation"}
	all := validation.NewErrors(issue)
	issue.Path[0] = "changed"
	require.Equal(t, validation.Path{"name"}, all.Issues()[0].Path)
	require.Equal(t, "public explanation", all.FirstMessage())
	require.NotContains(t, all.Error(), "public explanation")
	var empty *validation.Errors
	require.Nil(t, empty.Issues())
	require.Equal(t, validation.ErrInvalid.Error(), empty.Error())
	require.ErrorIs(t, empty, validation.ErrInvalid)
	for _, v := range []*validation.Errors{empty, validation.NewErrors(), validation.NewErrors(validation.Issue{Code: "invalid", Message: "   "})} {
		require.Equal(t, "请求参数不正确", v.FirstMessage())
	}
}

func TestContextCopiesStatesAndInvalidatesAliases(t *testing.T) {
	path := validation.Path{"name"}
	states := map[string]validation.FieldState{
		"Name":  {Path: path, Present: true, Null: true, Decoded: true},
		"Alias": {Path: validation.Path{"name"}, Decoded: true},
	}
	c := validation.NewContext(states, 2)
	path[0] = "changed"
	delete(states, "Name")
	require.True(t, c.Present("Name"))
	require.True(t, c.Null("Name"))
	require.False(t, c.Present("missing"))
	require.False(t, c.Null("missing"))
	c.Record(validation.Issue{Path: validation.Path{"name"}, Code: "required"})
	require.True(t, c.Invalid("Name"))
	require.True(t, c.Invalid("Alias"))
	require.False(t, c.Invalid("missing"))
	require.False(t, c.Valid("Alias"))
}

func TestCheckPreservesPublicPathsAndPrivateFailures(t *testing.T) {
	newContext := func(decoded, null bool) *validation.Context {
		return validation.NewContext(map[string]validation.FieldState{
			"Items": {Path: validation.Path{"items"}, Decoded: decoded, Null: null},
		}, 2)
	}
	for _, tc := range []struct{ decoded, null bool }{{false, false}, {true, true}} {
		c := newContext(tc.decoded, tc.null)
		validation.Check(c, "Items", 1, func(int) error { t.Fatal("rule ran on undecoded/null input"); return nil })
		require.NoError(t, c.Err())
	}
	c := newContext(true, false)
	validation.Check(c, "Items", 1, func(int) error { return nil })
	require.NoError(t, c.Err())
	validation.Check(c, "Items", 1, func(int) error {
		return validation.NewErrors(validation.Issue{Path: validation.Path{0, "name"}, Code: "required", Message: "name required"})
	})
	require.Equal(t, validation.Path{"items", 0, "name"}, c.Issues()[0].Path)
	require.True(t, c.Invalid("Items"))
	private := errors.New("private backend cause")
	for _, failure := range []error{private, fmt.Errorf("schema: %w", validation.ErrSchema), fmt.Errorf("wrapped: %w", validation.NewErrors(validation.Issue{Code: "invalid"}))} {
		c := newContext(true, false)
		validation.Check(c, "Items", 1, func(int) error { return failure })
		require.Same(t, failure, c.Err())
		require.Nil(t, c.Issues())
	}
	for _, field := range []string{"Items", "Unknown"} {
		c := newContext(true, false)
		validation.Check[int](c, field, 1, nil)
		require.ErrorIs(t, c.Err(), validation.ErrSchema)
	}
}

func TestContextMergeAndTerminalFailures(t *testing.T) {
	c := validation.NewContext(nil, 2)
	c.Merge(nil)
	c.Merge(validation.NewErrors(validation.Issue{Code: "first"}))
	require.Len(t, c.Issues(), 1)
	c.Record(validation.Issue{Code: "invalid code"})
	c.Record(validation.Issue{Code: "second"})
	require.ErrorIs(t, c.Err(), validation.ErrSchema)
	require.Nil(t, c.Issues())
	private := errors.New("private")
	for _, failure := range []error{private, validation.NewErrors(validation.Issue{Code: "invalid code"})} {
		c := validation.NewContext(nil, 2)
		c.Merge(failure)
		require.Same(t, failure, c.Err())
		require.Nil(t, c.Issues())
	}
}
