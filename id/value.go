package id

import (
	"encoding/json"
	"errors"
	"strconv"
)

// ID transports a non-negative signed BIGINT as a decimal string in JSON.
// Zero is valid; use validation rules such as gt=0 for required business IDs.
type ID int64

// Parse accepts only canonical decimal text in the range 0..MaxInt64.
func Parse(text string) (ID, error) {
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, errors.New("id: invalid decimal ID")
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, errors.New("id: invalid decimal ID")
		}
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, errors.New("id: decimal ID out of range")
	}
	return ID(n), nil
}

// Int64 returns the database representation.
func (v ID) Int64() int64 { return int64(v) }

// MarshalJSON preserves JavaScript integer precision.
func (v ID) MarshalJSON() ([]byte, error) {
	if v < 0 {
		return nil, errors.New("id: negative ID")
	}
	return json.Marshal(strconv.FormatInt(int64(v), 10))
}

// UnmarshalJSON requires a canonical decimal JSON string.
func (v *ID) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	return v.UnmarshalText([]byte(text))
}

// UnmarshalText supports query/path decoders using encoding.TextUnmarshaler.
func (v *ID) UnmarshalText(raw []byte) error {
	n, err := Parse(string(raw))
	if err == nil {
		*v = n
	}
	return err
}
