package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/validation"
)

func inputFailure(status int) error {
	return &httperror.Error{Status: status, Message: "请求参数不正确"}
}
func setText(v reflect.Value, s string) bool {
	if v.Kind() == reflect.Pointer {
		n := reflect.New(v.Type().Elem())
		if !setText(n.Elem(), s) {
			return false
		}
		v.Set(n)
		return true
	}
	if v.Type() == preparedTime {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil {
			return false
		}
		v.Set(reflect.ValueOf(time.UnixMilli(n).UTC()))
		return true
	}
	if v.Type() == reflect.TypeFor[id.ID]() {
		n, err := id.Parse(s)
		if err != nil {
			return false
		}
		v.SetInt(n.Int64())
		return true
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
		return true
	case reflect.Bool:
		if s != "true" && s != "false" {
			return false
		}
		v.SetBool(s == "true")
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strings.HasPrefix(s, "+") {
			return false
		}
		n, e := strconv.ParseInt(s, 10, v.Type().Bits())
		if e != nil {
			return false
		}
		v.SetInt(n)
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strings.HasPrefix(s, "+") {
			return false
		}
		n, e := strconv.ParseUint(s, 10, v.Type().Bits())
		if e != nil {
			return false
		}
		v.SetUint(n)
		return true
	case reflect.Float32, reflect.Float64:
		n, e := strconv.ParseFloat(s, v.Type().Bits())
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		v.SetFloat(n)
		return true
	}
	return false
}

// Decode binds a fresh DTO and returns per-field presence even when conversion fails.
// Syntax, media-type and resource failures terminate without partial diagnostics.
func (p *Prepared[T]) Decode(w http.ResponseWriter, r *http.Request, params map[string]string) (T, map[string]validation.FieldState, error) {
	var out T
	states := map[string]validation.FieldState{}
	if e := r.Context().Err(); e != nil {
		return out, states, e
	}
	var data map[string]any
	if p.options.Source == JSON {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
			return out, states, inputFailure(415)
		}
		if r.Body == nil {
			return out, states, inputFailure(400)
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, p.options.MaxBytes))
		if e != nil {
			if r.Context().Err() != nil {
				return out, states, r.Context().Err()
			}
			var large *http.MaxBytesError
			if errors.As(e, &large) {
				return out, states, inputFailure(413)
			}
			return out, states, inputFailure(400)
		}
		if !utf8.Valid(raw) {
			return out, states, inputFailure(400)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		node, e := readPreparedJSON(dec, 0, p.options)
		if e != nil {
			return out, states, e
		}
		if _, e = dec.Token(); !errors.Is(e, io.EOF) {
			return out, states, inputFailure(400)
		}
		var ok bool
		data, ok = node.(map[string]any)
		if !ok {
			return out, states, inputFailure(400)
		}
	} else {
		data = map[string]any{}
		var values url.Values
		if p.options.Source == Form {
			var e error
			values, e = readForm(w, r, p.options.MaxBytes)
			if e != nil {
				return out, states, e
			}
		} else if p.options.Source == Query {
			if int64(len(r.URL.RawQuery)) > p.options.MaxBytes {
				return out, states, inputFailure(413)
			}
			var e error
			values, e = url.ParseQuery(r.URL.RawQuery)
			if e != nil {
				return out, states, inputFailure(400)
			}
		} else {
			values = url.Values{}
			var size int64
			for k, v := range params {
				size += int64(len(k) + len(v))
				if size > p.options.MaxBytes {
					return out, states, inputFailure(413)
				}
				values[k] = []string{v}
			}
		}
		if len(values) > p.options.MaxFields {
			return out, states, inputFailure(413)
		}
		for k, v := range values {
			if len(v) > p.options.MaxItems {
				return out, states, inputFailure(413)
			}
			data[k] = v
		}
	}
	issues := []validation.Issue{}
	bindPreparedObject(reflect.ValueOf(&out).Elem(), p.fields, data, "", nil, states, &issues, p.options.Source)
	if e := r.Context().Err(); e != nil {
		return out, states, e
	}
	if len(issues) > 0 {
		return out, states, validation.NewErrors(issues...)
	}
	return out, states, nil
}
func readPreparedJSON(dec *json.Decoder, depth int, o Options) (any, error) {
	tok, e := dec.Token()
	if e != nil {
		return nil, inputFailure(400)
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if depth >= o.MaxDepth {
		return nil, inputFailure(413)
	}
	if d == '{' {
		obj := map[string]any{}
		for dec.More() {
			k, e := dec.Token()
			if e != nil {
				return nil, inputFailure(400)
			}
			key, ok := k.(string)
			if !ok {
				return nil, inputFailure(400)
			}
			if _, exists := obj[key]; exists {
				return nil, inputFailure(400)
			}
			if len(obj) >= o.MaxFields {
				return nil, inputFailure(413)
			}
			v, e := readPreparedJSON(dec, depth+1, o)
			if e != nil {
				return nil, e
			}
			obj[key] = v
		}
		end, e := dec.Token()
		if e != nil || end != json.Delim('}') {
			return nil, inputFailure(400)
		}
		return obj, nil
	}
	if d == '[' {
		arr := []any{}
		for dec.More() {
			if len(arr) >= o.MaxItems {
				return nil, inputFailure(413)
			}
			v, e := readPreparedJSON(dec, depth+1, o)
			if e != nil {
				return nil, e
			}
			arr = append(arr, v)
		}
		end, e := dec.Token()
		if e != nil || end != json.Delim(']') {
			return nil, inputFailure(400)
		}
		return arr, nil
	}
	return nil, inputFailure(400)
}
func joinGo(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
func bindPreparedObject(v reflect.Value, fs []Field, data map[string]any, prefix string, path validation.Path, states map[string]validation.FieldState, issues *[]validation.Issue, source Source) {
	names := map[string]bool{}
	for _, f := range fs {
		names[f.Path[0].(string)] = true
	}
	unknown := false
	for k := range data {
		if !names[k] {
			unknown = true
		}
	}
	if unknown {
		*issues = append(*issues, validation.Issue{Code: "validation", Message: "请求包含未知参数"})
	}
	for _, f := range fs {
		name := f.Path[0].(string)
		raw, present := data[name]
		gp := joinGo(prefix, f.GoPath)
		fp := append(append(validation.Path{}, path...), name)
		state := validation.FieldState{Path: fp, Label: f.Label, Present: present}
		field := v.FieldByIndex(f.Index)
		if !present {
			if f.HasDefault {
				state.Decoded = setText(field, f.Default)
			}
			if source == Path {
				state.Invalid = true
				*issues = append(*issues, validation.Issue{Path: fp, Code: "required", Message: f.Label + "不能为空"})
			}
			states[gp] = state
			continue
		}
		state.Null = raw == nil
		if f.Optional {
			field.FieldByName("Present").SetBool(true)
			field.FieldByName("Null").SetBool(state.Null)
			field = field.FieldByName("Value")
		}
		var valid bool
		if raw == nil {
			valid = (f.Optional && f.Nullable) || (!f.Optional && field.Kind() == reflect.Pointer)
		} else {
			valid = bindPreparedValue(field, raw, f, gp, fp, states, issues, source)
		}
		state.Decoded = valid && !state.Null
		state.Invalid = !valid
		states[gp] = state
		if !valid {
			*issues = append(*issues, validation.Issue{Path: fp, Code: "type", Message: f.Label + "格式不正确"})
		}
	}
}
func bindPreparedValue(v reflect.Value, raw any, f Field, gp string, path validation.Path, states map[string]validation.FieldState, issues *[]validation.Issue, source Source) bool {
	if v.Kind() == reflect.Pointer {
		n := reflect.New(v.Type().Elem())
		ok := bindPreparedValue(n.Elem(), raw, f, gp, path, states, issues, source)
		if ok {
			v.Set(n)
		}
		return ok
	}
	if source != JSON {
		ss := raw.([]string)
		if v.Kind() != reflect.Slice {
			return len(ss) == 1 && setText(v, ss[0])
		}
		v.Set(reflect.MakeSlice(v.Type(), len(ss), len(ss)))
		for i, s := range ss {
			ip := append(append(validation.Path{}, path...), i)
			key := gp + "[" + strconv.Itoa(i) + "]"
			ok := setText(v.Index(i), s)
			states[key] = validation.FieldState{Path: ip, Label: f.Label, Present: true, Decoded: ok, Invalid: !ok}
			if !ok {
				*issues = append(*issues, validation.Issue{Path: ip, Code: "type", Message: f.Label + "格式不正确"})
			}
		}
		return true
	}
	if v.Kind() == reflect.Struct && v.Type() != preparedTime {
		obj, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		bindPreparedObject(v, f.Children, obj, gp, path, states, issues, source)
		return true
	}
	if v.Kind() == reflect.Slice {
		arr, ok := raw.([]any)
		if !ok {
			return false
		}
		v.Set(reflect.MakeSlice(v.Type(), len(arr), len(arr)))
		for i, item := range arr {
			ip := append(append(validation.Path{}, path...), i)
			key := gp + "[" + strconv.Itoa(i) + "]"
			valid := false
			if item == nil {
				valid = v.Index(i).Kind() == reflect.Pointer
			} else {
				valid = bindPreparedValue(v.Index(i), item, f, key, ip, states, issues, source)
			}
			states[key] = validation.FieldState{Path: ip, Label: f.Label, Present: true, Null: item == nil, Decoded: valid && item != nil, Invalid: !valid}
			if !valid {
				*issues = append(*issues, validation.Issue{Path: ip, Code: "type", Message: f.Label + "格式不正确"})
			}
		}
		return true
	}
	if v.Type() == reflect.TypeFor[id.ID]() {
		s, ok := raw.(string)
		return ok && setText(v, s)
	}
	if f.stringJSON {
		s, ok := raw.(string)
		if !ok {
			return false
		}
		if v.Kind() == reflect.String {
			var unquoted string
			if json.Unmarshal([]byte(s), &unquoted) != nil {
				return false
			}
			v.SetString(unquoted)
			return true
		}
		return setText(v, s)
	}
	switch val := raw.(type) {
	case string:
		if v.Kind() != reflect.String {
			return false
		}
		v.SetString(val)
		return true
	case bool:
		if v.Kind() != reflect.Bool {
			return false
		}
		v.SetBool(val)
		return true
	case json.Number:
		if v.Kind() == reflect.String || v.Kind() == reflect.Bool {
			return false
		}
		return setText(v, string(val))
	}
	return false
}
