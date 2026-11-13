package respond

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// MillisTime explicitly encodes Unix milliseconds as a JSON integer.
// Construct with MillisTime(t), convert back with time.Time(m). Precision below a
// millisecond is discarded on marshal. Use *MillisTime for nullable fields.
type MillisTime time.Time

// MarshalJSON emits whole Unix milliseconds.
func (t MillisTime) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, time.Time(t).UnixMilli(), 10), nil
}

// UnmarshalJSON accepts whole Unix milliseconds only.
func (t *MillisTime) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("MillisTime requires an integer")
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*t = MillisTime(time.UnixMilli(n).UTC())
	return nil
}
