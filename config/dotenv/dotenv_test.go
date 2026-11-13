package dotenv_test

import (
	"os"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/config/dotenv"
)

func TestLiteralSource(t *testing.T) {
	t.Setenv("FRAME_DOTENV_TEST", "original")
	source, err := dotenv.Parse(strings.NewReader("# comment\nFRAME_DOTENV_TEST=replaced\nEMPTY=\nZERO=0\nQUOTED=\"a=b # literal\"\nLITERAL='${HOME}'\nLIST=[1,2]\n"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"FRAME_DOTENV_TEST": "replaced", "EMPTY": "", "ZERO": "0", "QUOTED": "a=b # literal", "LITERAL": "${HOME}", "LIST": "[1,2]"} {
		got, ok := source.Lookup(key)
		if !ok || got != want {
			t.Errorf("%s = %q, %v", key, got, ok)
		}
	}
	if _, ok := source.Lookup("MISSING"); ok {
		t.Fatal("absent key present")
	}
	if os.Getenv("FRAME_DOTENV_TEST") != "original" {
		t.Fatal("process env changed")
	}
}

func TestRejectMalformed(t *testing.T) {
	for _, input := range []string{"a", "export A=1", "A=1\nA=2", "A='unterminated-secret", "1KEY=a", "A=\"x\" trailing", "A=secret\x00value", "A=" + strings.Repeat("x", 1<<20)} {
		_, err := dotenv.Parse(strings.NewReader(input))
		if err == nil {
			t.Errorf("accepted malformed input")
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Fatal("leaked value")
		}
	}
	if _, err := dotenv.Parse(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}
