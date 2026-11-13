package request

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/maxtanforge/go-frame/validation"
)

// Source selects one explicit request input source.
type Source string

// Input sources a prepared DTO binds from.
const (
	Query Source = "query"
	JSON  Source = "json"
	Path  Source = "path"
	Form  Source = "form"
)

// Options sets preparation limits. Zero selects the documented defaults.
type Options struct {
	Source                        Source
	MaxBytes                      int64
	MaxDepth, MaxItems, MaxFields int
}

// Field is a read-only snapshot of one field's compiled input declaration.
// Children describe relative struct fields, including slice element structs.
type Field struct {
	GoPath       string
	Path         validation.Path
	Type         reflect.Type
	Rules, Label string
	Nullable     bool
	Children     []Field
	Index        []int
	Optional     bool
	Default      string
	HasDefault   bool
	stringJSON   bool
}

// Prepared is an immutable decoder safe for concurrent requests.
type Prepared[T any] struct {
	options Options
	fields  []Field
}

var preparedTime = reflect.TypeFor[time.Time]()
var preparedDuration = reflect.TypeFor[time.Duration]()

func optionalType(t reflect.Type) bool {
	return t.PkgPath() == "github.com/maxtanforge/go-frame/optional" && strings.HasPrefix(t.Name(), "Field[")
}
func inputType(t reflect.Type) reflect.Type {
	if optionalType(t) {
		t = t.Field(2).Type
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
func schemaError(s string) error { return fmt.Errorf("%w: %s", ErrSchema, s) }

// Prepare checks the DTO and compiles names, defaults and bounded nested fields.
func Prepare[T any](o Options) (*Prepared[T], error) {
	if o.Source != Query && o.Source != JSON && o.Source != Path && o.Source != Form {
		return nil, schemaError("invalid source")
	}
	if o.MaxBytes < 0 || o.MaxDepth < 0 || o.MaxItems < 0 || o.MaxFields < 0 {
		return nil, schemaError("limits must be positive")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 4096
		if o.Source == JSON {
			o.MaxBytes = 1 << 20
		}
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = 16
	}
	if o.MaxItems == 0 {
		o.MaxItems = 1000
	}
	if o.MaxFields == 0 {
		o.MaxFields = 64
	}
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		return nil, schemaError("DTO must be a struct")
	}
	count := 0
	fs, e := prepareFields(t, o, map[reflect.Type]bool{}, 0, &count)
	if e != nil {
		return nil, e
	}
	return &Prepared[T]{o, fs}, nil
}
func prepareFields(t reflect.Type, o Options, seen map[reflect.Type]bool, depth int, count *int) ([]Field, error) {
	if seen[t] || depth >= o.MaxDepth {
		return nil, schemaError("recursive or overly deep DTO")
	}
	seen[t] = true
	defer delete(seen, t)
	fs := []Field{}
	names := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if _, ok := sf.Tag.Lookup("validate"); ok {
			return nil, schemaError(sf.Name + ": use v instead of validate")
		}
		if _, ok := sf.Tag.Lookup("default"); ok {
			return nil, schemaError(sf.Name + ": use d instead of default")
		}
		jt := strings.Split(sf.Tag.Get("json"), ",")
		if jt[0] == "-" {
			for _, tag := range []string{"d", "v", "query", "path", "nullable"} {
				if _, ok := sf.Tag.Lookup(tag); ok {
					return nil, schemaError(sf.Name + ": excluded field has tags")
				}
			}
			continue
		}
		if !sf.IsExported() {
			return nil, schemaError(sf.Name + ": field is not writable")
		}
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && sf.Tag.Get("json") == "" && sf.Tag.Get(string(o.Source)) == "" {
			// Flattened reusable value structs cannot carry container rules or custom validation.
			for _, tag := range []string{"v", "d", "label", "nullable"} {
				if _, ok := sf.Tag.Lookup(tag); ok {
					return nil, schemaError(sf.Name + ": tags on flattened embedding unsupported")
				}
			}
			custom := reflect.TypeFor[interface{ Validate(*validation.Context) }]()
			if sf.Type.Implements(custom) || reflect.PointerTo(sf.Type).Implements(custom) {
				return nil, schemaError(sf.Name + ": custom validator on flattened embedding unsupported")
			}

			children, e := prepareFields(sf.Type, o, seen, depth+1, count)
			if e != nil {
				return nil, e
			}
			for _, c := range children {
				c.Index = append([]int{i}, c.Index...)
				c.GoPath = sf.Name + "." + c.GoPath
				n := c.Path[0].(string)
				if names[n] {
					return nil, schemaError("duplicate name " + n)
				}
				names[n] = true
				fs = append(fs, c)
			}
			continue
		}
		*count++
		if *count > o.MaxFields {
			return nil, schemaError("too many fields")
		}
		name := snakeName(sf.Name)
		if jt[0] != "" {
			name = jt[0]
		}
		if o.Source != JSON {
			if n, ok := sf.Tag.Lookup(string(o.Source)); ok {
				name = n
			}
		}
		if !namePattern.MatchString(name) || names[name] {
			return nil, schemaError(sf.Name + ": invalid or duplicate name")
		}
		names[name] = true
		f := Field{GoPath: sf.Name, Path: validation.Path{name}, Type: sf.Type, Rules: sf.Tag.Get("v"), Label: sf.Tag.Get("label"), Index: []int{i}, Optional: optionalType(sf.Type)}
		if f.Label == "" {
			f.Label = name
		}
		if !validation.ValidIssue(validation.Issue{Path: f.Path, Code: "type", Message: f.Label + "格式不正确"}) {
			return nil, schemaError(sf.Name + ": invalid label")
		}
		f.Default, f.HasDefault = sf.Tag.Lookup("d")
		if n, ok := sf.Tag.Lookup("nullable"); ok {
			if !f.Optional || (n != "true" && n != "false") {
				return nil, schemaError(sf.Name + ": invalid nullable")
			}
			f.Nullable = n == "true"
		}
		for _, j := range jt[1:] {
			if j == "string" {
				f.stringJSON = true
			} else if j != "omitempty" && j != "omitzero" && j != "" {
				return nil, schemaError(sf.Name + ": unsupported JSON option")
			}
		}
		typ := inputType(sf.Type)
		if typ == preparedDuration {
			return nil, schemaError(sf.Name + ": duration requires explicit unit integer")
		}
		if sf.Type.Kind() == reflect.Pointer && typ.Kind() == reflect.Pointer {
			return nil, schemaError(sf.Name + ": multiple pointers unsupported")
		}
		if f.Optional && o.Source != JSON {
			return nil, schemaError(sf.Name + ": optional requires JSON")
		}
		if typ.Kind() == reflect.Slice {
			if o.Source == Path {
				return nil, schemaError(sf.Name + ": path slice unsupported")
			}
			typ = typ.Elem()
			if typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
		}
		if typ == preparedDuration {
			return nil, schemaError(sf.Name + ": duration requires explicit unit integer")
		}
		if typ.Kind() == reflect.Struct && typ != preparedTime {
			if o.Source != JSON {
				return nil, schemaError(sf.Name + ": nested text DTO unsupported")
			}
			children, e := prepareFields(typ, o, seen, depth+1, count)
			if e != nil {
				return nil, e
			}
			f.Children = children
		} else if !scalarType(typ) {
			return nil, schemaError(sf.Name + ": unsupported type")
		}
		if f.stringJSON && (!scalarType(inputType(sf.Type)) || inputType(sf.Type) == preparedTime) {
			return nil, schemaError(sf.Name + ": string option requires scalar")
		}
		if f.HasDefault {
			if o.Source == Path || f.Optional || !scalarType(inputType(sf.Type)) {
				return nil, schemaError(sf.Name + ": default requires scalar")
			}
			for _, r := range strings.Split(f.Rules, ",") {
				if r == "present" {
					return nil, schemaError(sf.Name + ": present conflicts with default")
				}
			}
			v := reflect.New(sf.Type).Elem()
			if !setText(v, f.Default) {
				return nil, schemaError(sf.Name + ": invalid default")
			}
		}
		fs = append(fs, f)
	}
	return fs, nil
}
func scalarType(t reflect.Type) bool {
	if t == preparedTime {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
func snakeName(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) && i > 0 && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]) && !(i+1 == len(r)-1 && r[i+1] == 's'))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// Fields returns a recursively independent metadata snapshot.
func (p *Prepared[T]) Fields() []Field { return copyFields(p.fields) }
func copyFields(fs []Field) []Field {
	out := append([]Field{}, fs...)
	for i := range out {
		out[i].Path = append(validation.Path{}, fs[i].Path...)
		out[i].Index = append([]int{}, fs[i].Index...)
		out[i].Children = copyFields(fs[i].Children)
	}
	return out
}
