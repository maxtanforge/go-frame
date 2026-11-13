package tags_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/maxtanforge/go-frame/validation/tags"
	"github.com/stretchr/testify/require"
)

type completeInput struct {
	Name  string `v:"required,min=3" label:"名称"`
	Price int64  `v:"gte=0" label:"价格"`
}

func TestPreparedDefaultsAndOptional(t *testing.T) {
	_, err := tags.Prepare(reflect.TypeFor[struct {
		Size int `d:"0" v:"gte=1"`
	}]())
	require.ErrorIs(t, err, validation.ErrSchema)
	type patch struct {
		Flag  optional.Field[bool] `v:"required"`
		Other *bool                `v:"required"`
	}
	p, err := tags.Prepare(reflect.TypeFor[patch]())
	require.NoError(t, err)
	no := false
	require.NoError(t, p.Validate(&patch{Other: &no}, nil))
	require.ErrorIs(t, p.Validate(&patch{Other: &no, Flag: optional.Field[bool]{Present: true, Null: true}}, nil), validation.ErrInvalid)
}

func (v *completeInput) Validate(c *validation.Context) {
	if c.Decoded("Price") && v.Price < 0 {
		c.Add("Price", "business", "业务价格不合法")
	}
}

func TestPreparedAllRulesAndCustom(t *testing.T) {
	p, err := tags.Prepare(reflect.TypeFor[completeInput]())
	require.NoError(t, err)
	err = p.Validate(&completeInput{Price: -1}, nil)
	var all *validation.Errors
	require.ErrorAs(t, err, &all)
	require.Len(t, all.Issues(), 4)
	require.Equal(t, "名称不能为空", all.Issues()[0].Message)
}
func TestPreparedRejectsUnknownRulesBeforeServing(t *testing.T) {
	_, err := tags.Prepare(reflect.TypeFor[struct {
		Name string `v:"required_if=Other x"`
	}]())
	require.ErrorIs(t, err, validation.ErrSchema)
}

func TestPreparedRejectsMalformedNumericOneof(t *testing.T) {
	_, err := tags.Prepare(reflect.TypeFor[struct {
		N int `v:"oneof=0 nope"`
	}]())
	require.ErrorIs(t, err, validation.ErrSchema)
}
func TestPreparedDiveSkipsInvalidElement(t *testing.T) {
	type input struct {
		IDs []int `v:"dive,min=1"`
	}
	p, err := tags.Prepare(reflect.TypeFor[input]())
	require.NoError(t, err)
	states := map[string]validation.FieldState{"IDs": {Path: validation.Path{"ids"}, Present: true, Decoded: true}, "IDs[0]": {Path: validation.Path{"ids", 0}, Present: true, Invalid: true}}
	require.NoError(t, p.Validate(&input{IDs: []int{0}}, states))
}
func TestPreparedPointerOmitEmptyUsesPointerPresence(t *testing.T) {
	type input struct {
		N *int `v:"omitempty,min=1"`
	}
	p, err := tags.Prepare(reflect.TypeFor[input]())
	require.NoError(t, err)
	n := 0
	require.ErrorIs(t, p.Validate(&input{N: &n}, nil), validation.ErrInvalid)
}
func TestPreparedWorkloadBound(t *testing.T) {
	type input struct {
		IDs []int `v:"dive,min=1"`
	}
	p, err := tags.Prepare(reflect.TypeFor[input]())
	require.NoError(t, err)
	require.Error(t, p.Validate(&input{IDs: make([]int, 65537)}, nil))
}

type orderedChild struct {
	N int `v:"min=1"`
}

func (c *orderedChild) Validate(ctx *validation.Context) { ctx.Add("N", "child", "child custom") }

type orderedParent struct {
	Child orderedChild
	N     int `v:"min=1"`
}

func (p *orderedParent) Validate(ctx *validation.Context) {
	if ctx.Valid("N") {
		ctx.Add("N", "unexpected", "invalid state was lost")
	}
	ctx.Add("N", "parent", "parent custom")
}
func TestPreparedCollectSeparatesCustomOrder(t *testing.T) {
	p, e := tags.Prepare(reflect.TypeFor[orderedParent]())
	require.NoError(t, e)
	fields, custom := p.Collect(&orderedParent{}, nil)
	var f, c *validation.Errors
	require.ErrorAs(t, fields, &f)
	require.ErrorAs(t, custom, &c)
	require.Len(t, f.Issues(), 2)
	require.Len(t, c.Issues(), 2)
	require.Equal(t, "child", c.Issues()[0].Code)
	require.Equal(t, "parent", c.Issues()[1].Code)
}
func TestPreparedDefaultTimeAndPointer(t *testing.T) {
	_, e := tags.Prepare(reflect.TypeFor[struct {
		At time.Time `d:"1234"`
		N  *int      `d:"0" v:"required"`
	}]())
	require.NoError(t, e)
}
func TestPreparedCustomBudgetAndWorkload(t *testing.T) {
	p, e := tags.Prepare(reflect.TypeFor[orderedParent](), tags.Options{MaxCustomIssues: 1})
	require.NoError(t, e)
	require.ErrorIs(t, p.Validate(&orderedParent{}, nil), validation.ErrLimit)
	p, e = tags.Prepare(reflect.TypeFor[struct {
		IDs []int `v:"dive,min=1"`
	}](), tags.Options{MaxWork: 4})
	require.NoError(t, e)
	require.ErrorIs(t, p.Validate(&struct {
		IDs []int `v:"dive,min=1"`
	}{IDs: []int{1, 2, 3}}, nil), tags.ErrLimit)
}

func TestPreparedInvalidStateSuppressesPresent(t *testing.T) {
	type input struct {
		N optional.Field[int] `v:"present"`
	}
	p, e := tags.Prepare(reflect.TypeFor[input]())
	require.NoError(t, e)
	states := map[string]validation.FieldState{"N": {Path: validation.Path{"n"}, Present: true, Null: true, Invalid: true}}
	require.NoError(t, p.Validate(&input{N: optional.Field[int]{Present: true, Null: true}}, states))
}
func TestPreparedPresentDefaultConflictAfterOmitEmpty(t *testing.T) {
	_, e := tags.Prepare(reflect.TypeFor[struct {
		N int `d:"0" v:"omitempty,present"`
	}]())
	require.ErrorIs(t, e, validation.ErrSchema)
}

type LegacyPreparedInput struct{ N int }

func (*LegacyPreparedInput) Validate() error { return nil }
func TestPreparedRejectsLegacyCustomMethod(t *testing.T) {
	_, e := tags.Prepare(reflect.TypeFor[LegacyPreparedInput]())
	require.ErrorIs(t, e, validation.ErrSchema)
}

type EmbeddedCustomInput struct{ N int }

func (*EmbeddedCustomInput) Validate(*validation.Context) {}
func TestPreparedRejectsFlattenedCustomMethod(t *testing.T) {
	_, e := tags.Prepare(reflect.TypeFor[struct{ EmbeddedCustomInput }]())
	require.ErrorIs(t, e, validation.ErrSchema)
}

type EmbeddedPair struct {
	A int
	B int
}

func TestPreparedFlattenedFieldBudget(t *testing.T) {
	_, e := tags.Prepare(reflect.TypeFor[struct{ EmbeddedPair }](), tags.Options{MaxFields: 2})
	require.NoError(t, e)
}

type WrappedCustom struct{ N int }

func (*WrappedCustom) Validate(c *validation.Context) {
	c.Merge(errors.Join(validation.NewErrors(validation.Issue{Code: "business", Message: "private"}), context.DeadlineExceeded))
}
func TestPreparedPreservesWrappedCustomFailure(t *testing.T) {
	p, e := tags.Prepare(reflect.TypeFor[WrappedCustom]())
	require.NoError(t, e)
	require.ErrorIs(t, p.Validate(&WrappedCustom{}, nil), context.DeadlineExceeded)
}
