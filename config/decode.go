package config

import (
	"encoding"
	"errors"
	"reflect"
	"regexp"
	"strings"
)

// Source supplies canonical paths or declared environment keys, exclusively.
type Source struct {
	Name   string
	Values map[string]string
	Lookup func(string) (string, bool)
}

// Entry describes the winning source and a safely formatted effective value.
type Entry struct {
	Path, Env, Source, Value string
	Secret                   bool
}

// Result contains a decoded value and deterministic field-order provenance.
// Ordinary configuration slices are copied. Excluded fields and custom text
// types' internals remain caller-owned and must be kept immutable.
type Result[T any] struct {
	Value   T
	Entries []Entry
}

const maxFields = 4096
const maxDepth = 32
const maxValueBytes = 64 * 1024

var segmentPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
var textType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

type fieldSpec struct {
	index     []int
	path, env string
	secret    bool
}

// Decode applies sources in order, validates every supplied value, then calls the
// root Validate method if present. Errors never disclose input values or custom errors.
func Decode[T any](defaults T, sources ...Source) (Result[T], error) {
	var zero Result[T]
	if len(sources) > maxFields {
		return zero, errors.New("config: source count limit exceeded")
	}
	prepared, err := Prepare(defaults)
	if err != nil {
		return zero, err
	}
	return prepared.Decode(sources...)
}

// Prepared captures a schema and defaults for repeated decoding. Ordinary slices
// are copied; excluded fields and custom text type internals remain caller-owned
// and must remain immutable, including from validators. Use Prepare to construct it.
type Prepared[T any] struct {
	defaults reflect.Value
	fields   []fieldSpec
}

// Prepare checks the schema and finite defaults and captures ordinary slices.
// It does not call validators, text decoders or formatters. Required values may
// be supplied later; every Decode validates the complete configuration.
func Prepare[T any](defaults T) (*Prepared[T], error) {
	original := reflect.ValueOf(defaults)
	if !original.IsValid() || original.Kind() != reflect.Struct {
		return nil, errors.New("config: root must be a struct")
	}
	var fields []fieldSpec
	paths, envs := map[string]bool{}, map[string]bool{}
	if err := scanSchema(original.Type(), nil, "", "", false, 0, &fields, paths, envs); err != nil {
		return nil, err
	}
	for _, f := range fields {
		if !finiteValue(original.FieldByIndex(f.index)) {
			return nil, &FieldError{Path: f.path, Env: f.env, Source: "defaults", Expected: expectedType(original.FieldByIndex(f.index).Type()), Rule: RuleFinite}
		}
	}
	return &Prepared[T]{defaults: cloneConfig(original), fields: fields}, nil
}

// Entries returns independent field-order metadata for capturing declared
// environment keys. Values are always empty: defaults are never formatted here.
func (p *Prepared[T]) Entries() []Entry {
	if p == nil {
		return nil
	}
	entries := make([]Entry, len(p.fields))
	for i, f := range p.fields {
		entries[i] = Entry{Path: f.path, Env: f.env, Source: "defaults", Secret: f.secret}
	}
	return entries
}

// Decode applies sources to a fresh copy of captured defaults, then calls the
// root validator once. It has the same source checks and safe diagnostics as Decode.
func (p *Prepared[T]) Decode(sources ...Source) (Result[T], error) {
	var zero Result[T]
	if p == nil || !p.defaults.IsValid() {
		return zero, errors.New("config: decoder is not prepared")
	}
	if len(sources) > maxFields {
		return zero, errors.New("config: source count limit exceeded")
	}
	value := cloneConfig(p.defaults)
	fields, entries := p.fields, p.Entries()
	leafPaths := make(map[string]bool, len(fields))
	for _, f := range fields {
		leafPaths[f.path] = true
	}
	for _, source := range sources {
		if !segmentPattern.MatchString(source.Name) || len(source.Name) > 64 {
			return zero, errors.New("config: invalid source name")
		}
		if source.Values != nil && source.Lookup != nil {
			return zero, errors.New("config: source must use values or lookup exclusively")
		}
		if len(source.Values) > maxFields {
			return zero, errors.New("config: too many source values")
		}
		for path := range source.Values {
			if !leafPaths[path] {
				return zero, errors.New("config: unknown configuration path")
			}
		}
		for i, f := range fields {
			raw, ok := source.Values[f.path]
			if source.Lookup != nil && f.env != "" {
				raw, ok = source.Lookup(f.env)
			}
			if !ok {
				continue
			}
			if len(raw) > maxValueBytes {
				return zero, &FieldError{Path: f.path, Env: f.env, Source: source.Name, Expected: expectedType(value.FieldByIndex(f.index).Type()), Rule: RuleSize}
			}
			dst := value.FieldByIndex(f.index)
			decoded, err := parseValue(dst.Type(), raw)
			if err != nil {
				return zero, &FieldError{Path: f.path, Env: f.env, Source: source.Name, Expected: expectedType(dst.Type()), Rule: RuleType}
			}
			dst.Set(decoded)
			entries[i].Source = source.Name
		}
	}
	if valid, ok := value.Addr().Interface().(interface{ Validate() error }); ok {
		if err := valid.Validate(); err != nil {
			return zero, validationDiagnostic(err, fields, entries, func(i int) string { return expectedType(value.FieldByIndex(fields[i].index).Type()) })
		}
	}
	for i, f := range fields {
		entries[i].Value = formatValue(value.FieldByIndex(f.index), f.secret)
	}
	return Result[T]{Value: value.Interface().(T), Entries: entries}, nil
}
func scanSchema(t reflect.Type, index []int, path, prefix string, secret bool, depth int, fields *[]fieldSpec, paths, envs map[string]bool) error {
	if depth > maxDepth || len(paths) > maxFields {
		return errors.New("config: schema depth exceeded")
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" || sf.Tag.Get("config") == "-" {
			continue
		}
		name, tagged := sf.Tag.Lookup("config")
		if !tagged {
			name = strings.ToLower(sf.Name)
		}
		if !segmentPattern.MatchString(name) {
			return errors.New("config: invalid schema path")
		}
		p := name
		if path != "" {
			p = path + "." + name
		}
		ix := append(append([]int(nil), index...), i)
		sec := secret || sf.Tag.Get("secret") == "true"
		typ := sf.Type
		if typ.Kind() == reflect.Struct && !reflect.PointerTo(typ).Implements(textType) {
			if paths[p] {
				return errors.New("config: duplicate schema name")
			}
			paths[p] = true
			if len(paths) > maxFields {
				return errors.New("config: schema field limit exceeded")
			}
			if err := scanSchema(typ, ix, p, prefix+sf.Tag.Get("envPrefix"), sec, depth+1, fields, paths, envs); err != nil {
				return err
			}
			continue
		}
		if !supported(typ) {
			return errors.New("config: unsupported schema type")
		}
		env := sf.Tag.Get("env")
		if env != "" {
			env = prefix + env
			if !segmentPattern.MatchString(env) {
				return errors.New("config: invalid environment name")
			}
		}
		if paths[p] || (env != "" && envs[env]) {
			return errors.New("config: duplicate schema name")
		}
		paths[p] = true
		if len(paths) > maxFields {
			return errors.New("config: schema field limit exceeded")
		}
		if env != "" {
			envs[env] = true
		}
		*fields = append(*fields, fieldSpec{ix, p, env, sec})
		if len(*fields) > maxFields {
			return errors.New("config: schema field limit exceeded")
		}
	}
	return nil
}
func supported(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Interface:
		return false
	}
	if reflect.PointerTo(t).Implements(textType) {
		return true
	}
	if t.Kind() == reflect.Slice {
		return (t.Elem().Kind() != reflect.Slice || reflect.PointerTo(t.Elem()).Implements(textType)) && supported(t.Elem())
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// Clone slices recursively; excluded fields remain caller-owned and are never decoded.
func cloneConfig(v reflect.Value) reflect.Value {
	out := reflect.New(v.Type()).Elem()
	out.Set(v)
	if reflect.PointerTo(v.Type()).Implements(textType) {
		return out
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if out.Field(i).CanSet() && v.Type().Field(i).PkgPath == "" && v.Type().Field(i).Tag.Get("config") != "-" {
				out.Field(i).Set(cloneConfig(v.Field(i)))
			}
		}
	case reflect.Slice:
		if !v.IsNil() {
			out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(cloneConfig(v.Index(i)))
			}
		}
	}
	return out
}
