package yaml_test

import (
	"strings"
	"testing"

	configyaml "github.com/maxtanforge/go-frame/config/yaml"
)

func TestParse(t *testing.T) {
	source, err := configyaml.Parse(strings.NewReader("service:\n  name: demo\nbusiness:\n  enabled: false\n  count: 0x10\n  hosts: [one, 'two,three']\n  empty: ''\n"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"service.name": "demo", "business.enabled": "false", "business.count": "16", "business.hosts": `["one","two,three"]`, "business.empty": ""} {
		if source.Values[key] != want {
			t.Errorf("%s = %q; want %q", key, source.Values[key], want)
		}
	}
}

func TestRejectUnsafeDocuments(t *testing.T) {
	for name, input := range map[string]string{
		"empty": "", "null": "key: null", "duplicate": "key: a\nkey: b", "alias": "a: &a secret\nb: *a", "multiple": "a: b\n---\na: c", "nonstring": "1: value", "dotted": "a.b: c", "nestedlist": "a: [[1]]", "custom": "a: !secret password", "maplist": "a: [{b: c}]", "emptymap": "a: {}", "nan": "a: .nan", "large": "a: " + strings.Repeat("x", 1<<20), "syntax": "a: [super-secret-value",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := configyaml.Parse(strings.NewReader(input))
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(err.Error(), "super-secret-value") {
				t.Fatal("error leaked input")
			}
		})
	}
	if _, err := configyaml.Parse(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := configyaml.Parse(strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}
}

func TestDepthAndDuplicateFlattenedPaths(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(strings.Repeat(" ", i))
		b.WriteString("key:\n")
	}
	b.WriteString(strings.Repeat(" ", 40) + "value: x\n")
	if _, err := configyaml.Parse(strings.NewReader(b.String())); err == nil {
		t.Fatal("unbounded nesting accepted")
	}
}

func TestOversizedIntegerDoesNotBecomeRoundedFloat(t *testing.T) {
	if _, err := configyaml.Parse(strings.NewReader("id: 18446744073709551616")); err == nil {
		t.Fatal("integer beyond uint64 accepted with lost precision")
	}
}
