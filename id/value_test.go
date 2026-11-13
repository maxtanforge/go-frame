package id

import (
	"encoding/json"
	"testing"
)

func TestIDWirePrecisionAndCanonicalText(t *testing.T) {
	for _, text := range []string{"0", "9007199254740993", "9223372036854775807"} {
		v, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(v)
		if err != nil || string(raw) != `"`+text+`"` {
			t.Fatalf("%s %v", raw, err)
		}
		var got ID
		if err := json.Unmarshal(raw, &got); err != nil || got != v {
			t.Fatalf("%d %v", got, err)
		}
	}
	for _, text := range []string{"", "01", "-1", "-0", "+1", " 1", "1.0", "1e2", "１２", "9223372036854775808"} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	for _, raw := range []string{"1", "null", "true", `"01"`} {
		v := ID(42)
		if err := json.Unmarshal([]byte(raw), &v); err == nil || v != 42 {
			t.Fatalf("accepted/modified %s: %d", raw, v)
		}
	}
	if _, err := json.Marshal(ID(-1)); err == nil {
		t.Fatal("negative output accepted")
	}
}
