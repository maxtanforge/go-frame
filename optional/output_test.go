package optional_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/optional"
)

func TestRequestFieldCannotLeakThroughResponse(t *testing.T) {
	input := struct {
		Secret optional.Field[string] `json:"secret"`
	}{Secret: optional.Field[string]{Present: true, Value: "private-value"}}
	data, err := json.Marshal(input)
	if err == nil || len(data) != 0 {
		t.Fatalf("request-only field must fail response encoding, got %s, %v", data, err)
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatal("encoding error must not disclose input values")
	}
}
