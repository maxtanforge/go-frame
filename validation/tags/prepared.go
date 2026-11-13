package tags

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-playground/validator/v10"
	"github.com/maxtanforge/go-frame/validation"
)

// Options configures immutable public messages. Overrides use Go field path +
// "." + rule, for example Name.required. No raw values are interpolated.
type Options struct {
	Language                                                string
	Messages                                                map[string]string
	MaxWork, MaxCustomIssues, MaxDepth, MaxFields, MaxRules int
}

// ErrLimit rejects workloads that cannot be validated completely.
var ErrLimit = validation.ErrLimit

type rule struct {
	name, param string
	children    []rule
}
type fieldPlan struct {
	nullable    bool
	index       []int
	name, label string
	rules       []rule
	node        *valuePlan
}
type valuePlan struct {
	typ      reflect.Type
	optional bool
	fields   []fieldPlan
	elem     *valuePlan
}

// Prepared is an immutable compiled validation plan, safe for concurrent use.
type Prepared struct {
	root       *valuePlan
	engine     *validator.Validate
	options    Options
	fieldCount int
}

// Prepare compiles one DTO type; declaration errors fail here, not per request.
func Prepare(typ reflect.Type, options ...Options) (*Prepared, error) {
	p := &Prepared{engine: validator.New(validator.WithRequiredStructEnabled())}
	if len(options) > 1 {
		return nil, validation.ErrSchema
	}
	if len(options) == 1 {
		p.options = options[0]
	}
	if p.options.MaxWork < 0 || p.options.MaxCustomIssues < 0 || p.options.MaxDepth < 0 || p.options.MaxFields < 0 || p.options.MaxRules < 0 {
		return nil, validation.ErrSchema
	}
	if p.options.MaxWork == 0 {
		p.options.MaxWork = 65536
	}
	if p.options.MaxCustomIssues == 0 {
		p.options.MaxCustomIssues = 4096
	}
	if p.options.MaxDepth == 0 {
		p.options.MaxDepth = 16
	}
	if p.options.MaxFields == 0 {
		p.options.MaxFields = 64
	}
	if p.options.MaxRules == 0 {
		p.options.MaxRules = 32
	}
	if p.options.Language != "" && p.options.Language != "zh" && p.options.Language != "en" {
		return nil, validation.ErrSchema
	}
	msgs := map[string]string{}
	for k, v := range p.options.Messages {
		if !validation.ValidIssue(validation.Issue{Code: "validation", Message: v}) {
			return nil, validation.ErrSchema
		}
		msgs[k] = v
	}
	p.options.Messages = msgs
	if typ == nil {
		return nil, validation.ErrSchema
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, validation.ErrSchema
	}
	var err error
	p.root, err = p.compile(typ, map[reflect.Type]bool{}, 0)
	return p, err
}

func optionalType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.PkgPath() == "github.com/maxtanforge/go-frame/optional" && strings.HasPrefix(t.Name(), "Field[")
}
func baseType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if optionalType(t) {
		f, _ := t.FieldByName("Value")
		return baseType(f.Type)
	}
	return t
}
func (p *Prepared) compile(t reflect.Type, stack map[reflect.Type]bool, depth int) (*valuePlan, error) {
	if depth > p.options.MaxDepth {
		return nil, validation.ErrSchema
	}
	legacy := reflect.TypeFor[interface{ Validate() error }]()
	if t.Implements(legacy) || (t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(legacy)) {
		return nil, validation.ErrSchema
	}
	n := &valuePlan{typ: t}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if optionalType(t) {
		n.optional = true
		f, _ := t.FieldByName("Value")
		t = f.Type
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
	}
	if t == reflect.TypeFor[time.Time]() {
		return n, nil
	}
	if t.Kind() == reflect.Slice {
		var err error
		n.elem, err = p.compile(t.Elem(), stack, depth+1)
		return n, err
	}
	if t.Kind() != reflect.Struct {
		switch t.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			return n, nil
		}
		return nil, validation.ErrSchema
	}
	if stack[t] {
		return nil, validation.ErrSchema
	}
	stack[t] = true
	defer delete(stack, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup("validate"); ok {
			return nil, validation.ErrSchema
		}
		if strings.Split(f.Tag.Get("json"), ",")[0] == "-" {
			continue
		}
		// Flattened embeds support reusable fields, not container rules or custom callbacks.
		if f.Anonymous && f.Tag.Get("json") == "" {
			for _, tag := range []string{"v", "d", "label", "nullable"} {
				if _, ok := f.Tag.Lookup(tag); ok {
					return nil, validation.ErrSchema
				}
			}
			custom := reflect.TypeFor[interface{ Validate(*validation.Context) }]()
			if f.Type.Implements(custom) || (f.Type.Kind() != reflect.Pointer && reflect.PointerTo(f.Type).Implements(custom)) {
				return nil, validation.ErrSchema
			}
		}
		if f.PkgPath != "" {
			return nil, validation.ErrSchema
		}
		if !(f.Anonymous && f.Tag.Get("json") == "") {
			p.fieldCount++
		}
		if p.fieldCount > p.options.MaxFields {
			return nil, validation.ErrSchema
		}
		if nullable, ok := f.Tag.Lookup("nullable"); ok {
			if !optionalType(f.Type) || (nullable != "true" && nullable != "false") {
				return nil, validation.ErrSchema
			}
		}
		child, err := p.compile(f.Type, stack, depth+1)
		if err != nil {
			return nil, err
		}
		rs, err := p.rules(f.Tag.Get("v"), f.Type)
		if err != nil {
			return nil, fmt.Errorf("%w: %s.%s", validation.ErrSchema, t.Name(), f.Name)
		}
		if d, ok := f.Tag.Lookup("d"); ok {
			if err := p.checkDefault(f.Type, d, rs); err != nil {
				return nil, fmt.Errorf("%w: invalid default for %s", validation.ErrSchema, f.Name)
			}
		}
		label := f.Tag.Get("label")
		if label == "" {
			label = fieldName(f)
		}
		if !validation.ValidIssue(validation.Issue{Code: "validation", Message: label}) {
			return nil, validation.ErrSchema
		}
		if f.Anonymous && f.Tag.Get("json") == "" && len(rs) == 0 {

			for _, nested := range child.fields {
				nested.index = append([]int{i}, nested.index...)
				nested.name = f.Name + "." + nested.name
				n.fields = append(n.fields, nested)
			}
			continue
		}
		n.fields = append(n.fields, fieldPlan{index: []int{i}, name: f.Name, label: label, rules: rs, node: child, nullable: f.Tag.Get("nullable") == "true"})
	}
	return n, nil
}
func (p *Prepared) rules(raw string, t reflect.Type) ([]rule, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > p.options.MaxRules {
		return nil, validation.ErrSchema
	}
	t = baseType(t)
	out := []rule{}
	for i, part := range parts {
		pair := strings.SplitN(part, "=", 2)
		r := rule{name: pair[0]}
		if len(pair) == 2 {
			r.param = pair[1]
		}
		if r.name == "dive" {
			if t.Kind() != reflect.Slice || len(pair) != 1 {
				return nil, validation.ErrSchema
			}
			var err error
			r.children, err = p.rules(strings.Join(parts[i+1:], ","), t.Elem())
			if err != nil {
				return nil, err
			}
			return append(out, r), nil
		}
		switch r.name {
		case "present", "required", "omitempty":
			if len(pair) != 1 {
				return nil, validation.ErrSchema
			}
		case "email", "uuid":
			if t.Kind() != reflect.String || len(pair) != 1 {
				return nil, validation.ErrSchema
			}
		case "oneof":
			if r.param == "" || !(t.Kind() == reflect.String || isNumber(t.Kind())) {
				return nil, validation.ErrSchema
			}
			if isNumber(t.Kind()) {
				for _, part := range strings.Fields(r.param) {
					var err error
					switch t.Kind() {
					case reflect.Float32, reflect.Float64:
						var f float64
						f, err = strconv.ParseFloat(part, 64)
						if math.IsNaN(f) || math.IsInf(f, 0) {
							err = validation.ErrSchema
						}
					case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
						_, err = strconv.ParseUint(part, 10, 64)
					default:
						_, err = strconv.ParseInt(part, 10, 64)
					}
					if err != nil {
						return nil, validation.ErrSchema
					}
				}
			}
		case "min", "max", "len", "gt", "gte", "lt", "lte":
			v, err := strconv.ParseFloat(r.param, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, validation.ErrSchema
			}
			if !isNumber(t.Kind()) && t.Kind() != reflect.String && t.Kind() != reflect.Slice {
				return nil, validation.ErrSchema
			}
			if t.Kind() != reflect.Float32 && t.Kind() != reflect.Float64 {
				if _, err := strconv.ParseInt(r.param, 10, 64); err != nil {
					return nil, validation.ErrSchema
				}
			}
		default:
			return nil, validation.ErrSchema
		}
		if r.name != "present" && r.name != "omitempty" {
			if err := p.probe(reflect.Zero(t).Interface(), part); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, nil
}
func isNumber(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Float64 && k != reflect.Uintptr
}
func (p *Prepared) probe(value any, tag string) error { _, err := p.evaluate(value, tag); return err }

// evaluate recovers only third-party configuration panics, never user callbacks.
func (p *Prepared) evaluate(value any, tag string) (valid bool, err error) {
	defer func() {
		if recover() != nil {
			valid = false
			err = validation.ErrSchema
		}
	}()
	e := p.engine.Var(value, tag)
	if e == nil {
		return true, nil
	}
	if _, ok := e.(validator.ValidationErrors); !ok { //nolint:errorlint // 引擎自身输出,无包装层
		return false, validation.ErrSchema
	}
	return false, nil
}

// Validate collects field diagnostics followed by custom diagnostics.
func (p *Prepared) Validate(value any, states map[string]validation.FieldState) error {
	fields, custom := p.Collect(value, states)
	var issues []validation.Issue
	for _, err := range []error{fields, custom} {
		if err == nil {
			continue
		}
		es, ok := err.(*validation.Errors) //nolint:errorlint // 校验引擎自身输出,不含业务包装
		if !ok || errors.Is(err, validation.ErrSchema) {
			return err
		}
		issues = append(issues, es.Issues()...)
	}
	if len(issues) > 0 {
		return validation.NewErrors(issues...)
	}
	return nil
}

// Collect separates field-rule errors from custom errors. A nil state map
// validates a directly constructed DTO as supplied input.
func (p *Prepared) Collect(value any, states map[string]validation.FieldState) (error, error) {
	if p == nil || p.root == nil {
		return validation.ErrSchema, nil
	}
	v := reflect.ValueOf(value)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return validation.ErrSchema, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Type() != baseType(p.root.typ) {
		return validation.ErrSchema, nil
	}
	if !v.CanAddr() {
		addressable := reflect.New(v.Type()).Elem()
		addressable.Set(v)
		v = addressable
	}
	budget := p.options.MaxWork
	if !p.workload(p.root, v, &budget) {
		return ErrLimit, nil
	}
	if states == nil {
		states = map[string]validation.FieldState{}
		p.states(p.root, v, "", nil, states)
	} else {
		clone := map[string]validation.FieldState{}
		for k, s := range states {
			s.Path = append(validation.Path{}, s.Path...)
			clone[k] = s
		}
		states = clone
	}
	c := validation.NewContext(states, p.options.MaxWork)
	p.walk(p.root, v, "", states, c)
	fieldErr := c.Err()
	if fieldErr != nil && !errors.Is(fieldErr, validation.ErrInvalid) {
		return fieldErr, nil
	}
	pathKeys := map[string][]string{}
	for key, state := range states {
		encoded, err := json.Marshal(state.Path)
		if err != nil {
			return validation.ErrSchema, nil
		}
		pathKeys[string(encoded)] = append(pathKeys[string(encoded)], key)
	}
	for _, issue := range c.Issues() {
		encoded, err := json.Marshal(issue.Path)
		if err != nil {
			return validation.ErrSchema, nil
		}
		for _, key := range pathKeys[string(encoded)] {
			state := states[key]
			state.Invalid = true
			states[key] = state
		}
	}

	custom := validation.NewContext(states, p.options.MaxCustomIssues)
	p.custom(p.root, v, "", states, custom)
	return fieldErr, custom.Err()
}
func (p *Prepared) workload(n *valuePlan, v reflect.Value, budget *int) bool {
	*budget--
	if *budget < 0 {
		return false
	}
	v = deref(v)
	if !v.IsValid() {
		return true
	}
	if n.optional {
		if !v.FieldByName("Present").Bool() || v.FieldByName("Null").Bool() {
			return true
		}
		v = deref(v.FieldByName("Value"))
		if !v.IsValid() {
			return true
		}
	}
	if n.elem != nil {
		for i := 0; i < v.Len(); i++ {
			if !p.workload(n.elem, v.Index(i), budget) {
				return false
			}
		}
		return true
	}
	for _, f := range n.fields {
		fv := fieldValue(v, f.index)
		if !ruleWork(f.rules, fv, budget) || !p.workload(f.node, fv, budget) {
			return false
		}
	}
	return true
}
func ruleWork(rules []rule, v reflect.Value, budget *int) bool {
	v = deref(v)
	if v.IsValid() && optionalType(v.Type()) {
		v = deref(v.FieldByName("Value"))
	}
	for _, r := range rules {
		*budget--
		if *budget < 0 {
			return false
		}
		if r.name == "dive" && v.IsValid() {
			for i := 0; i < v.Len(); i++ {
				if !ruleWork(r.children, v.Index(i), budget) {
					return false
				}
			}
		}
	}
	return true
}
func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}
func fieldValue(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		v = deref(v)
		if !v.IsValid() {
			return v
		}
		v = v.Field(i)
	}
	return v
}
func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}
func (p *Prepared) states(n *valuePlan, v reflect.Value, key string, path validation.Path, out map[string]validation.FieldState) {
	v = deref(v)
	if !v.IsValid() {
		return
	}
	if n.optional {
		if !v.FieldByName("Present").Bool() || v.FieldByName("Null").Bool() {
			return
		}
		v = deref(v.FieldByName("Value"))
		if !v.IsValid() {
			return
		}
	}
	if n.elem != nil {
		for i := 0; i < v.Len(); i++ {
			ik := fmt.Sprintf("%s[%d]", key, i)
			ip := append(append(validation.Path{}, path...), i)
			null := !deref(v.Index(i)).IsValid()
			out[ik] = validation.FieldState{Path: ip, Present: true, Null: null, Decoded: !null}
			p.states(n.elem, v.Index(i), ik, ip, out)
		}
		return
	}
	for _, f := range n.fields {
		fv := fieldValue(v, f.index)
		name := join(key, f.name)
		fp := append(append(validation.Path{}, path...), f.label)
		// Labels are for messages only; use the declared JSON/default wire name.
		t := v.Type()
		var sf reflect.StructField
		for _, idx := range f.index {
			for t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			sf = t.Field(idx)
			t = sf.Type
		}
		fp[len(fp)-1] = fieldName(sf)
		s := validation.FieldState{Path: fp, Label: f.label, Present: true, Decoded: fv.IsValid()}
		unwrapped := deref(fv)
		if !unwrapped.IsValid() {
			s.Null = true
		}
		if f.node.optional && unwrapped.IsValid() {
			s.Present = unwrapped.FieldByName("Present").Bool()
			s.Null = unwrapped.FieldByName("Null").Bool()
			s.Decoded = s.Present && !s.Null
		}
		out[name] = s
		p.states(f.node, fv, name, fp, out)
	}
}
func (p *Prepared) walk(n *valuePlan, v reflect.Value, key string, states map[string]validation.FieldState, c *validation.Context) {
	v = deref(v)
	if !v.IsValid() {
		return
	}
	if n.optional {
		if !v.FieldByName("Present").Bool() || v.FieldByName("Null").Bool() {
			return
		}
		v = deref(v.FieldByName("Value"))
		if !v.IsValid() {
			return
		}
	}
	if n.elem != nil {
		for i := 0; i < v.Len(); i++ {
			p.walk(n.elem, v.Index(i), fmt.Sprintf("%s[%d]", key, i), states, c)
		}
		return
	}
	for _, f := range n.fields {
		fv := fieldValue(v, f.index)
		name := join(key, f.name)
		s, ok := states[name]
		if !ok {
			continue
		}
		label := f.label
		if s.Label != "" {
			label = s.Label
		}
		if f.node.optional && s.Present && s.Null && !f.nullable && !s.Invalid {
			c.Add(name, "required", label+"不能为 null")
		}
		p.apply(f.rules, fv, name, s, label, states, c)
		if s.Decoded && !s.Null {
			p.walk(f.node, fv, name, states, c)
		}
	}
}
func (p *Prepared) custom(n *valuePlan, v reflect.Value, key string, states map[string]validation.FieldState, c *validation.Context) {
	v = deref(v)
	if !v.IsValid() {
		return
	}
	if n.optional {
		if !v.FieldByName("Present").Bool() || v.FieldByName("Null").Bool() {
			return
		}
		v = deref(v.FieldByName("Value"))
		if !v.IsValid() {
			return
		}
	}
	if n.elem != nil {
		for i := 0; i < v.Len(); i++ {
			ik := fmt.Sprintf("%s[%d]", key, i)
			s, ok := states[ik]
			if ok && s.Decoded && !s.Null {
				p.custom(n.elem, v.Index(i), ik, states, c)
			}
		}
		return
	}
	for _, f := range n.fields {
		name := join(key, f.name)
		s, ok := states[name]
		if ok && s.Decoded && !s.Null {
			p.custom(f.node, fieldValue(v, f.index), name, states, c)
		}
	}
	if v.CanAddr() {
		if custom, ok := v.Addr().Interface().(interface{ Validate(*validation.Context) }); ok {
			if key == "" {
				custom.Validate(c)
			} else {
				sub := c.Scope(key)
				custom.Validate(sub)
				c.Merge(sub.Err())
			}
		}
	}
}

func (p *Prepared) apply(rules []rule, v reflect.Value, key string, s validation.FieldState, label string, states map[string]validation.FieldState, c *validation.Context) {
	if s.Invalid {
		return
	}
	original := v
	v = deref(v)
	optional := v.IsValid() && optionalType(v.Type())
	if v.IsValid() && optionalType(v.Type()) {
		v = deref(v.FieldByName("Value"))
	}
	for _, r := range rules {
		if r.name == "present" {
			if !s.Present || s.Null {
				c.Add(key, "required", p.message(key, label, r, v))
			}
			continue
		}
		if s.Invalid {
			return
		}
		if optional && (!s.Present || s.Null) {
			continue
		}
		if r.name == "omitempty" {
			if !v.IsValid() || (v.IsZero() && (original.Kind() != reflect.Pointer || original.IsNil())) || s.Null || !s.Decoded {
				return
			}
			continue
		}
		if r.name == "required" {
			zero := !v.IsValid() || v.IsZero()
			if original.IsValid() && original.Kind() == reflect.Pointer {
				zero = original.IsNil()
			}
			if !v.IsValid() || s.Null || !s.Decoded || zero {
				c.Add(key, "required", p.message(key, label, r, v))
			}
			continue
		}
		if !s.Decoded || s.Null || !v.IsValid() {
			continue
		}
		if r.name == "dive" {
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i)
				ik := fmt.Sprintf("%s[%d]", key, i)
				is, ok := states[ik]
				if !ok {
					continue
				}
				p.apply(r.children, item, ik, is, label, states, c)
			}
			continue
		}
		tag := r.name
		if r.param != "" {
			tag += "=" + r.param
		}
		valid, err := p.evaluate(v.Interface(), tag)
		if err != nil {
			c.Merge(err)
			return
		}
		if !valid {
			c.Add(key, ruleCode(r.name), p.message(key, label, r, v))
		}
	}
}
func ruleCode(name string) string {
	switch name {
	case "gte", "gt", "min":
		return "min"
	case "lte", "lt", "max":
		return "max"
	case "len":
		return "length"
	case "oneof":
		return "enum"
	}
	return "format"
}
func (p *Prepared) message(key, label string, r rule, v reflect.Value) string {
	if m := p.options.Messages[key+"."+r.name]; m != "" {
		return m
	}
	if p.options.Language == "en" {
		switch r.name {
		case "present":
			return label + " must be provided"
		case "required":
			return label + " is required"
		}
		return label + " must satisfy " + r.name + " " + r.param
	}
	switch r.name {
	case "present":
		return label + "必须传入"
	case "required":
		return label + "不能为空"
	case "email":
		return label + "必须是有效邮箱"
	case "uuid":
		return label + "必须是有效 UUID"
	case "oneof":
		return label + "不在允许范围内"
	}
	unit := ""
	if v.IsValid() {
		switch v.Kind() {
		case reflect.String:
			unit = " 个字符"
		case reflect.Slice:
			unit = " 项"
		}
	}
	switch r.name {
	case "min", "gte":
		return label + "不能小于 " + r.param + unit
	case "max", "lte":
		return label + "不能大于 " + r.param + unit
	case "gt":
		return label + "必须大于 " + r.param + unit
	case "lt":
		return label + "必须小于 " + r.param + unit
	case "len":
		return label + "必须为 " + r.param + unit
	}
	return label + "格式不正确"
}
func fieldName(f reflect.StructField) string {
	if name := strings.Split(f.Tag.Get("json"), ",")[0]; name != "" {
		return name
	}
	r := []rune(f.Name)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) && i > 0 && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]) && !(i+1 == len(r)-1 && r[i+1] == 's'))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}
