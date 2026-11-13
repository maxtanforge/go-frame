package config

import "testing"

type preparedSettings struct {
	Secret string   `config:"secret" env:"SECRET" secret:"true"`
	Labels []string `config:"labels"`
	Calls  *int     `config:"-"`
}

func (s preparedSettings) Validate() error {
	*s.Calls++
	if s.Secret == "" {
		return Invalid("secret", RuleRequired)
	}
	return nil
}

func TestPrepareCapturesMetadataAndIndependentDefaults(t *testing.T) {
	calls := 0
	defaults := preparedSettings{Labels: []string{"original"}, Calls: &calls}
	p, err := Prepare(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("Prepare called validator")
	}
	entries := p.Entries()
	if entries[0].Env != "SECRET" || !entries[0].Secret || entries[0].Value != "" {
		t.Fatalf("unsafe metadata: %#v", entries[0])
	}
	entries[0].Env = "changed"
	defaults.Labels[0] = "changed"
	if p.Entries()[0].Env != "SECRET" {
		t.Fatal("metadata was mutable")
	}
	for i := 1; i <= 2; i++ {
		r, err := p.Decode(Source{Name: "override", Values: map[string]string{"secret": "supplied"}})
		if err != nil {
			t.Fatal(err)
		}
		if calls != i || r.Value.Labels[0] != "original" {
			t.Fatal("validation count or frozen defaults changed")
		}
		r.Value.Labels[0] = "returned mutation"
	}
	if _, err := Decode(defaults); err == nil || calls != 3 {
		t.Fatal("Decode must always validate")
	}
}

func TestUninitializedPreparedRejectsDecode(t *testing.T) {
	var p Prepared[preparedSettings]
	if _, err := p.Decode(); err == nil {
		t.Fatal("zero Prepared accepted")
	}
}

type prepareText string

func (*prepareText) UnmarshalText([]byte) error  { panic("unexpected text decoder") }
func (prepareText) MarshalText() ([]byte, error) { panic("unexpected text formatter") }
func (prepareText) String() string               { panic("unexpected formatter") }

func TestPrepareDoesNotInvokeCustomTextMethods(t *testing.T) {
	p, err := Prepare(struct{ Text prepareText }{Text: "private-default"})
	if err != nil {
		t.Fatal(err)
	}
	if entries := p.Entries(); len(entries) != 1 || entries[0].Value != "" {
		t.Fatal("defaults were formatted")
	}
	if _, err := p.Decode(); err != nil {
		t.Fatal(err)
	}
}
