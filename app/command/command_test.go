package command_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/app/command"
)

func TestDispatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "run:"}, {[]string{"run", "--config", "local.yaml"}, "run:--config local.yaml"}, {[]string{"config", "check", "--set", "port=0"}, "check:--set port=0"}, {[]string{"config", "explain"}, "explain:"},
	} {
		got := ""
		opts := command.Options{Run: func(_ context.Context, args []string) error { got = "run:" + strings.Join(args, " "); return nil }, Check: func(_ context.Context, args []string, explain bool) error {
			got = "check:"
			if explain {
				got = "explain:"
			}
			got += strings.Join(args, " ")
			return nil
		}}
		if err := command.Execute(context.Background(), tc.args, opts); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}

func TestVersionDoesNotLoad(t *testing.T) {
	var out bytes.Buffer
	called := false
	opts := command.Options{Out: &out, Build: command.BuildInfo{Version: "v1", Commit: "abc", BuildTime: "2026-09-28T00:00:00Z"}, Run: func(context.Context, []string) error { called = true; return nil }, Check: func(context.Context, []string, bool) error { called = true; return nil }}
	if err := command.Execute(context.Background(), []string{"version"}, opts); err != nil {
		t.Fatal(err)
	}
	if called || !strings.Contains(out.String(), `"version":"v1"`) || !strings.Contains(out.String(), `"go_version":"go`) {
		t.Fatalf("invalid output/callback: %s %v", out.String(), called)
	}
}

func TestInvalidAndErrors(t *testing.T) {
	for _, args := range [][]string{{"bad-secret"}, {"version", "unexpected"}, {"config"}, {"config", "bad"}} {
		called := false
		err := command.Execute(context.Background(), args, command.Options{Run: func(context.Context, []string) error { called = true; return nil }})
		if err == nil || called || strings.Contains(err.Error(), "bad-secret") {
			t.Fatalf("invalid command handling: %v", err)
		}
	}
	want := errors.New("callback")
	if err := command.Execute(context.Background(), nil, command.Options{Run: func(context.Context, []string) error { return want }}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := command.Execute(context.Background(), []string{"version"}, command.Options{Out: failedWriter{}}); err == nil {
		t.Fatal("lost writer failure")
	}
	if err := command.Execute(context.Background(), nil, command.Options{}); err == nil {
		t.Fatal("missing run accepted")
	}
	if err := command.Execute(nil, nil, command.Options{}); err == nil { //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
		t.Fatal("nil context accepted")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
