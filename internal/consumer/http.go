package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cancellationHarness = `package main
import("context";"io";"os";"example.com/service/internal/bootstrap";"github.com/maxtanforge/go-frame/app/command")
func main(){ctx,cancel:=context.WithCancel(context.Background());defer cancel();go func(){_,_ = io.Copy(io.Discard,os.Stdin);cancel()}();if bootstrap.Execute(ctx,os.Args[1:],io.Discard,func(string)(string,bool){return "",false},command.BuildInfo{})!=nil{os.Exit(1)}}
`

func processCheck(ctx context.Context, service, root, extension string, env []string, signal bool) error {
	dir := filepath.Join(service, "cmd", "acceptance")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(dir, "main.go"), []byte(cancellationHarness), 0600); e != nil {
		return e
	}
	binary := filepath.Join(root, "harness"+extension)
	if _, e := run(ctx, service, env, "go", "build", "-buildvcs=false", "-o", binary, "./cmd/acceptance"); e != nil {
		return e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if e = listener.Close(); e != nil {
		return e
	}
	prefix := filepath.Base(root)
	args := []string{"run", "--set", fmt.Sprintf("framework.http.port=%d", port), "--set", "host=127.0.0.1", "--set", "greeting.prefix=" + prefix}
	p, _, e := start(ctx, service, env, binary, args...)
	if e != nil {
		return e
	}
	defer func() {
		_ = p.stdin.Close()
		_ = p.cleanup()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.wait(cleanupCtx)
	}()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }, Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e = awaitReady(readyCtx, p.done, func() error { return checkHTTP(readyCtx, client, base, "GET", "/readyz", "", 200, "", false) }); e != nil {
		return e
	}
	for _, c := range []struct {
		method, path, body string
		code               int
		message            string
		envelope           bool
	}{{"GET", "/healthz", "", 200, "", false}, {"POST", "/greetings", `{"name":"Ada"}`, 200, prefix + ", Ada", true}, {"POST", "/greetings", `{"name":" "}`, 400, "", true}, {"POST", "/greetings", `{"name":"Ada","admin":true}`, 400, "", true}} {
		if e = checkHTTP(ctx, client, base, c.method, c.path, c.body, c.code, c.message, c.envelope); e != nil {
			return e
		}
	}
	select {
	case <-p.done:
		return errors.New("service exited before shutdown")
	default:
	}
	if signal {
		e = signalTerminate(p)
	} else {
		e = p.stdin.Close()
	}
	if e != nil {
		return e
	}
	shutdownCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if e = p.wait(shutdownCtx); e != nil {
		return errors.New("graceful shutdown failed")
	}
	request, e := http.NewRequestWithContext(shutdownCtx, "GET", base+"/healthz", nil)
	if e != nil {
		return e
	}
	response, e := client.Do(request)
	if e == nil {
		_ = response.Body.Close()
		return errors.New("listener survived shutdown")
	}
	return nil
}
func checkHTTP(ctx context.Context, client *http.Client, base, method, path, body string, code int, message string, envelope bool) error {
	request, e := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if e != nil {
		return e
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := client.Do(request)
	if e != nil {
		return errors.New("request failed")
	}
	defer func() { _ = response.Body.Close() }()
	b, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(b) > 65536 || response.StatusCode != code {
		return errors.New("unexpected response")
	}
	if envelope {
		var value struct {
			Success *bool `json:"success"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &value) != nil || value.Success == nil || *value.Success != (code == 200) || value.Data.Message != message {
			return errors.New("invalid response envelope")
		}
	} else {
		var value struct {
			Success *bool `json:"success"`
			Data    struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		expected := "ok"
		if path == "/readyz" {
			expected = "ready"
		}
		if json.Unmarshal(b, &value) != nil || value.Success == nil || !*value.Success || value.Data.Status != expected {
			return errors.New("invalid health response")
		}
	}
	return nil
}

func awaitReady(ctx context.Context, done <-chan struct{}, check func() error) error {
	for {
		select {
		case <-done:
			return errors.New("service exited before readiness")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if check() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errors.New("early exit")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
