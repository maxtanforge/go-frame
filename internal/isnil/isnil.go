// Package isnil reports typed nil values passed through interface arguments.
package isnil

import "reflect"

// Check reports whether v is nil or a nil value of a nilable kind
// (chan, func, interface, map, pointer, slice) boxed in an interface.
func Check(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}
