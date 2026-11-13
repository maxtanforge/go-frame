// Package optional provides presence-aware JSON request fields.
package optional

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Field distinguishes omitted, null and supplied JSON request properties.
// Decode each request into a fresh DTO: omitted properties are never visited
// by encoding/json. Field is an input type, not an output or persistence type.
type Field[T any] struct {
	Present bool
	Null    bool
	Value   T
}

// MarshalJSON rejects use of request presence fields in a response. Map the
// field to an explicit response value instead; input values are never disclosed.
func (Field[T]) MarshalJSON() ([]byte, error) {
	return nil, errors.New("optional: request field cannot be encoded as a response")
}

// UnmarshalJSON decodes exactly one value and rejects unknown struct fields.
// A failure resets the receiver so it cannot retain a successful prior value.
func (f *Field[T]) UnmarshalJSON(data []byte) error {
	*f = Field[T]{}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Present = true
		f.Null = true
		return nil
	}
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("optional: expected one JSON value")
	}
	f.Value = value
	f.Present = true
	return nil
}
