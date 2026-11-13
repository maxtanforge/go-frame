package request

import (
	"encoding"
	"encoding/xml"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var xmlSchemas sync.Map // reflect.Type -> xmlSchemaResult
type xmlSchemaResult struct{ err error }

var (
	xmlUnmarshaler     = reflect.TypeFor[xml.Unmarshaler]()
	xmlAttrUnmarshaler = reflect.TypeFor[xml.UnmarshalerAttr]()
	xmlTextUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
	errXMLSchemaProbe  = errors.New("XML schema probe complete")
)

// CheckXMLSchema checks native XML declarations without executing business
// methods. Custom XML/text decoders own their mapping and are treated as opaque.
// The result is cached by type; declaration failures wrap ErrSchema.
func CheckXMLSchema(t reflect.Type) error {
	if t == nil || t.Kind() != reflect.Struct {
		return ErrSchema
	}
	if cached, ok := xmlSchemas.Load(t); ok {
		return cached.(xmlSchemaResult).err
	}
	err := checkXMLType(t, "", map[reflect.Type]bool{})
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrSchema, err)
	}
	result, _ := xmlSchemas.LoadOrStore(t, xmlSchemaResult{err})
	return result.(xmlSchemaResult).err
}

func xmlImplements(t, iface reflect.Type) bool {
	return t.Implements(iface) || (t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(iface))
}

func checkXMLType(t reflect.Type, mode string, seen map[reflect.Type]bool) error {
	if mode == "innerxml" || mode == "comment" {
		if t.Kind() == reflect.String || (t.Kind() == reflect.Slice && reflect.TypeFor[[]byte]().AssignableTo(t)) {
			return nil
		}
		return fmt.Errorf("unsupported XML %s type %s", mode, t)
	}
	if xmlImplements(t, xmlTextUnmarshaler) || (mode == "attr" && xmlImplements(t, xmlAttrUnmarshaler)) || (mode == "" && xmlImplements(t, xmlUnmarshaler)) {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if t.Elem().Kind() == reflect.Pointer {
			return fmt.Errorf("unsupported XML %s pointer type %s", mode, t)
		}
		return checkXMLType(t.Elem(), mode, seen)
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return nil
	}
	if t.Kind() == reflect.Slice {
		if mode == "chardata" {
			return fmt.Errorf("unsupported XML character data type %s", t)
		}
		if seen[t] {
			return fmt.Errorf("recursive XML slice type %s", t)
		}
		seen[t] = true
		defer delete(seen, t)
		return checkXMLType(t.Elem(), mode, seen)
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Struct:
		if mode == "attr" && t == reflect.TypeFor[xml.Attr]() {
			return nil
		}
		if mode != "" {
			return fmt.Errorf("unsupported XML %s type %s", mode, t)
		}
		if t == reflect.TypeFor[xml.Name]() || seen[t] {
			return nil
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if (!f.IsExported() && !f.Anonymous) || f.Tag.Get("xml") == "-" || f.Name == "XMLName" {
				continue
			}
			if f.Anonymous && !f.IsExported() && f.Type.Kind() == reflect.Pointer {
				return fmt.Errorf("cannot initialize unexported XML embedding %s.%s", t, f.Name)
			}
			mode := ""
			for _, option := range strings.Split(f.Tag.Get("xml"), ",")[1:] {
				switch option {
				case "attr", "chardata", "cdata", "innerxml", "comment":
					mode = option
				}
			}
			if mode == "cdata" {
				mode = "chardata"
			}
			if err := checkXMLType(f.Type, mode, seen); err != nil {
				return fmt.Errorf("%s.%s: %w", t, f.Name, err)
			}
		}
		// Check pointer shapes first: native XMLName lookup also dereferences
		// field types and would loop forever on a recursive named pointer.
		return probeXMLMapping(t)
	default:
		return fmt.Errorf("unsupported XML type %s", t)
	}
}

// Only a start token is supplied: encoding/xml constructs its native field
// mapping before requesting content. The next token fails immediately, before
// attributes, children, character data or business field decoders can run.
type xmlSchemaProbe struct{ started bool }

func (p *xmlSchemaProbe) Token() (xml.Token, error) {
	if !p.started {
		p.started = true
		return xml.StartElement{Name: xml.Name{Local: "schema-probe"}}, nil
	}
	return nil, errXMLSchemaProbe
}

func probeXMLMapping(t reflect.Type) (err error) {
	// This probe may assign XMLName or initialize embedded pointers, but never
	// receives attributes, content or an end token that can run field decoders.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("unsupported XML declaration %s: %v", t, recovered)
		}
	}()
	if err := checkXMLEmbedding(t, map[reflect.Type]bool{}); err != nil {
		return err
	}
	// Including methods promoted from anonymous fields, the root must be native.
	if xmlImplements(t, xmlUnmarshaler) || xmlImplements(t, xmlTextUnmarshaler) {
		return fmt.Errorf("cannot probe custom XML decoder on %s", t)
	}
	err = xml.NewTokenDecoder(&xmlSchemaProbe{}).Decode(reflect.New(t).Interface())
	if errors.Is(err, errXMLSchemaProbe) {
		return nil
	}
	// A root-name mismatch is a value constraint, not a mapping failure.
	var rootMismatch xml.UnmarshalError
	if errors.As(err, &rootMismatch) {
		return nil
	}
	return err
}

func checkXMLEmbedding(t reflect.Type, path map[reflect.Type]bool) error {
	if path[t] {
		return fmt.Errorf("recursive XML embedding in %s", t)
	}
	path[t] = true
	defer delete(path, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous || f.Tag.Get("xml") == "-" {
			continue
		}
		t := f.Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() == reflect.Struct {
			if err := checkXMLEmbedding(t, path); err != nil {
				return err
			}
		}
	}
	return nil
}
