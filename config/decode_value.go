package config

import (
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var durationType = reflect.TypeOf(time.Duration(0))

func parseValue(t reflect.Type, raw string) (reflect.Value, error) {
	v := reflect.New(t).Elem()
	if decoder, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return v, decoder.UnmarshalText([]byte(raw))
	}
	if t.Kind() == reflect.Slice {
		raw = strings.TrimSpace(raw)
		var values []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' {
			return v, errors.New("list required")
		}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return v, err
		}
		v = reflect.MakeSlice(t, len(values), len(values))
		for i, item := range values {
			var s string
			if len(item) > 0 && item[0] == '"' {
				if err := json.Unmarshal(item, &s); err != nil {
					return v, err
				}
			} else {
				s = string(item)
				if s == "null" || len(s) == 0 || s[0] == '[' || s[0] == '{' {
					return v, errors.New("scalar required")
				}
			}
			elem, err := parseValue(t.Elem(), s)
			if err != nil {
				return v, err
			}
			v.Index(i).Set(elem)
		}
		return v, nil
	}
	if t == durationType {
		d, err := time.ParseDuration(raw)
		v.SetInt(int64(d))
		return v, err
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		n, err := strconv.ParseBool(raw)
		if err != nil {
			return v, err
		}
		v.SetBool(n)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, t.Bits())
		if err != nil {
			return v, err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, t.Bits())
		if err != nil {
			return v, err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, t.Bits())
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return v, errors.New("finite number required")
		}
		v.SetFloat(n)
	}
	return v, nil
}
func formatValue(v reflect.Value, secret bool) string {
	if secret {
		return "[REDACTED]"
	}
	if reflect.PointerTo(v.Type()).Implements(textType) {
		return "[custom]"
	}
	if v.Type() == durationType {
		return strconv.Quote(time.Duration(v.Int()).String())
	}
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits())
	case reflect.Slice:
		s := "["
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				s += ","
			}
			item := formatValue(v.Index(i), false)
			if item == "[custom]" {
				item = strconv.Quote(item)
			}
			s += item
		}
		return s + "]"
	}
	return "[unsupported]"
}

func finiteValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return !math.IsNaN(v.Float()) && !math.IsInf(v.Float(), 0)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !finiteValue(v.Index(i)) {
				return false
			}
		}
	}
	return true
}
func expectedType(t reflect.Type) string {
	if reflect.PointerTo(t).Implements(textType) {
		return "text value"
	}
	if t.Kind() == reflect.Slice {
		return "list of " + expectedType(t.Elem())
	}
	if t == durationType {
		return "duration"
	}
	return t.Kind().String()
}
