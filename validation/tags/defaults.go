package tags

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/maxtanforge/go-frame/validation"
)

func (p *Prepared) checkDefault(t reflect.Type, s string, rules []rule) error {
	for _, r := range rules {
		if r.name == "present" {
			return validation.ErrSchema
		}
	}
	v, err := defaultValue(t, s)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if r.name == "present" || r.name == "dive" {
			return validation.ErrSchema
		}
		if r.name == "omitempty" {
			valid, err := p.evaluate(v.Interface(), "required")
			if err != nil {
				return err
			}
			if !valid {
				return nil
			}
			continue
		}
		tag := r.name
		if r.param != "" {
			tag += "=" + r.param
		}
		valid, err := p.evaluate(v.Interface(), tag)
		if err != nil || !valid {
			return validation.ErrSchema
		}
	}
	return nil
}
func defaultValue(t reflect.Type, s string) (reflect.Value, error) {
	v := reflect.New(t).Elem()
	if t.Kind() == reflect.Pointer {
		child, err := defaultValue(t.Elem(), s)
		if err != nil {
			return v, err
		}
		v.Set(reflect.New(t.Elem()))
		v.Elem().Set(child)
		return v, nil
	}
	if t == reflect.TypeFor[time.Duration]() {
		return v, validation.ErrSchema
	}
	if t == reflect.TypeFor[time.Time]() {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil {
			return v, validation.ErrSchema
		}
		v.Set(reflect.ValueOf(time.UnixMilli(n).UTC()))
		return v, nil
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		if s != "true" && s != "false" {
			return v, validation.ErrSchema
		}
		v.SetBool(s == "true")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strings.HasPrefix(s, "+") {
			return v, validation.ErrSchema
		}
		n, e := strconv.ParseInt(s, 10, t.Bits())
		if e != nil {
			return v, validation.ErrSchema
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if strings.HasPrefix(s, "+") {
			return v, validation.ErrSchema
		}
		n, e := strconv.ParseUint(s, 10, t.Bits())
		if e != nil {
			return v, validation.ErrSchema
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, e := strconv.ParseFloat(s, t.Bits())
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return v, validation.ErrSchema
		}
		v.SetFloat(n)
	default:
		return v, validation.ErrSchema
	}
	return v, nil
}
