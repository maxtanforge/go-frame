package tags_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/validation"
	"github.com/maxtanforge/go-frame/validation/tags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedDefaultValueBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   reflect.Type
		value string
		valid bool
	}{
		{"string", reflect.TypeFor[string](), "hello", true},
		{"bool", reflect.TypeFor[bool](), "false", true},
		{"bool numeric", reflect.TypeFor[bool](), "1", false},
		{"int8 min", reflect.TypeFor[int8](), "-128", true},
		{"int8 overflow", reflect.TypeFor[int8](), "128", false},
		{"signed plus", reflect.TypeFor[int](), "+1", false},
		{"uint8 max", reflect.TypeFor[uint8](), "255", true},
		{"uint8 overflow", reflect.TypeFor[uint8](), "256", false},
		{"unsigned minus", reflect.TypeFor[uint](), "-1", false},
		{"unsigned plus", reflect.TypeFor[uint](), "+1", false},
		{"float", reflect.TypeFor[float32](), "1.25", true},
		{"float overflow", reflect.TypeFor[float32](), "1e100", false},
		{"nan", reflect.TypeFor[float64](), "NaN", false},
		{"infinity", reflect.TypeFor[float64](), "Inf", false},
		{"duration", reflect.TypeFor[time.Duration](), "1000", false},
		{"bad time", reflect.TypeFor[time.Time](), "yesterday", false},
		{"slice", reflect.TypeFor[[]int](), "1", false},
		{"pointer overflow", reflect.TypeFor[*int8](), "128", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: tc.typ, Tag: reflect.StructTag(`d:"` + tc.value + `"`)}})
			_, err := tags.Prepare(typ)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, validation.ErrSchema)
			}
		})
	}
}

func TestPreparedRejectsInvalidOptionsAndBudgets(t *testing.T) {
	typ := reflect.TypeFor[struct {
		Name string `v:"required,min=2"`
	}]()
	for _, options := range []tags.Options{
		{MaxWork: -1}, {MaxCustomIssues: -1}, {MaxDepth: -1}, {MaxFields: -1}, {MaxRules: -1},
		{Language: "unknown"}, {MaxRules: 1}, {Messages: map[string]string{"Name.required": "bad\nmessage"}},
	} {
		_, err := tags.Prepare(typ, options)
		require.ErrorIs(t, err, validation.ErrSchema)
	}
	_, err := tags.Prepare(typ, tags.Options{}, tags.Options{})
	require.ErrorIs(t, err, validation.ErrSchema)
	_, err = tags.Prepare(reflect.TypeFor[struct{ A, B int }](), tags.Options{MaxFields: 1})
	require.ErrorIs(t, err, validation.ErrSchema)
	_, err = tags.Prepare(reflect.TypeFor[struct{ Child struct{ N int } }](), tags.Options{MaxDepth: 1})
	require.ErrorIs(t, err, validation.ErrSchema)
}

func TestPreparedCopiesMessagesAndSupportsConcurrentValidation(t *testing.T) {
	type input struct {
		Name string `v:"required"`
	}
	messages := map[string]string{"Name.required": "name is missing"}
	p, err := tags.Prepare(reflect.TypeFor[input](), tags.Options{Language: "en", Messages: messages})
	require.NoError(t, err)
	messages["Name.required"] = "mutated"
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			var all *validation.Errors
			err := p.Validate(input{}, nil)
			if !assert.ErrorAs(t, err, &all) {
				return
			}
			assert.Equal(t, "name is missing", all.FirstMessage())
			assert.NoError(t, p.Validate(input{Name: "valid"}, nil))
		})
	}
	wg.Wait()
}

func TestPreparedRuleCodesAndMessages(t *testing.T) {
	for _, tc := range []struct{ rule, code, message string }{
		{"required", "required", "Name is required"},
		{"present", "required", "Name must be provided"},
		{"email", "format", "Name must satisfy email "},
		{"uuid", "format", "Name must satisfy uuid "},
		{"oneof=a b", "enum", "Name must satisfy oneof a b"},
		{"len=3", "length", "Name must satisfy len 3"},
		{"min=2", "min", "Name must satisfy min 2"},
		{"max=-1", "max", "Name must satisfy max -1"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Name", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`label:"Name" v:"` + tc.rule + `"`)}})
			p, err := tags.Prepare(typ, tags.Options{Language: "en"})
			require.NoError(t, err)
			states := map[string]validation.FieldState{"Name": {Path: validation.Path{"name"}, Decoded: true}}
			var all *validation.Errors
			require.ErrorAs(t, p.Validate(reflect.New(typ).Interface(), states), &all)
			require.Equal(t, []validation.Issue{{Path: validation.Path{"name"}, Code: tc.code, Message: tc.message}}, all.Issues())
		})
	}
}

func TestPreparedRejectsMismatchedAndNilValues(t *testing.T) {
	type input struct{ N int }
	p, err := tags.Prepare(reflect.TypeFor[input]())
	require.NoError(t, err)
	for _, value := range []any{nil, (*input)(nil), 1, struct{ Other int }{}} {
		require.ErrorIs(t, p.Validate(value, nil), validation.ErrSchema)
	}
	var absent *tags.Prepared
	require.ErrorIs(t, absent.Validate(input{}, nil), validation.ErrSchema)
}
