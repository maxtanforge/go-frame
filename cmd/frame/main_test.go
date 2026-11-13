package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestUsageOnlyAdvertisesDevelopment(t *testing.T) {
	var output bytes.Buffer
	if err := printUsage(&output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "new service") || !strings.Contains(output.String(), "frame dev") {
		t.Fatalf("unexpected usage: %s", output.String())
	}
}

func TestDevDefaultsAndExplicitArguments(t *testing.T) {
	o, err := parseDev(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Package != "./examples/configured" || len(o.Args) != 4 || o.Args[3] != "--watch-config" || o.Interval != 500*time.Millisecond {
		t.Fatalf("defaults: %+v", o)
	}
	o, err = parseDev([]string{"--package", ".", "--", "hello", "--literal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Args) != 2 || o.Args[1] != "--literal" {
		t.Fatalf("args: %v", o.Args)
	}
	o, err = parseDev([]string{"--package", "."})
	if err != nil || len(o.Args) != 0 {
		t.Fatalf("custom package got default args: %+v %v", o, err)
	}
	o, err = parseDev([]string{"--"})
	if err != nil || len(o.Args) != 0 {
		t.Fatalf("explicit empty args: %+v %v", o, err)
	}
}
func TestInvalidFlags(t *testing.T) {
	for _, args := range [][]string{{"--interval", "0s"}, {"--debounce", "-1s"}, {"--stop-timeout", "0s"}, {"--package", ""}, {"unexpected"}, {"--bogus"}} {
		if _, err := parseDev(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
