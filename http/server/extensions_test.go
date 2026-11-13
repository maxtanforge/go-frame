package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testTLSConfig(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}

type extensionContextKey string
type logMessages chan string

func (c logMessages) Write(p []byte) (int, error) {
	select {
	case c <- string(p):
	default:
	}
	return len(p), nil
}

func TestServerTLSEntryPointsAndHooks(t *testing.T) {
	for _, mode := range []string{"Serve", "Run"} {
		t.Run(mode, func(t *testing.T) {
			config, clientConfig := testTLSConfig(t)
			listening := make(chan net.Addr, 1)
			active := make(chan struct{}, 1)
			logs := make(logMessages, 8)
			logger := log.New(logs, "test ", 0)
			s, err := NewWithOptions("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/panic" {
					panic("hook panic")
				}
				if r.TLS == nil || r.Context().Value(extensionContextKey("base")) != "base" || r.Context().Value(extensionContextKey("conn")) != "conn" {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(204)
			}), Options{TLSConfig: config, ErrorLog: logger,
				BaseContext: func(l net.Listener) context.Context {
					listening <- l.Addr()
					return context.WithValue(context.Background(), extensionContextKey("base"), "base")
				},
				ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
					return context.WithValue(ctx, extensionContextKey("conn"), "conn")
				},
				ConnState: func(_ net.Conn, state http.ConnState) {
					if state == http.StateActive {
						select {
						case active <- struct{}{}:
						default:
						}
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if s.srv.TLSConfig == config || s.srv.ErrorLog != logger {
				t.Fatal("config not cloned or logger lost")
			}
			config.MinVersion = tls.VersionTLS13
			if s.srv.TLSConfig.MinVersion != tls.VersionTLS12 {
				t.Fatal("TLS scalar alias")
			}
			done := make(chan error, 1)
			if mode == "Serve" {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				go func() { done <- s.Serve(ln) }()
			} else {
				go func() { done <- s.Run() }()
			}
			t.Cleanup(func() {
				_ = s.Close()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("server did not stop")
				}
			})
			var addr net.Addr
			select {
			case addr = <-listening:
			case <-time.After(3 * time.Second):
				t.Fatal("not listening")
			}
			transport := &http.Transport{TLSClientConfig: clientConfig}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			resp, err := client.Get("https://" + addr.String())
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 204 {
				t.Fatal(resp.StatusCode)
			}
			select {
			case <-active:
			case <-time.After(time.Second):
				t.Fatal("missing state notification")
			}
			resp, err = client.Get("https://" + addr.String() + "/panic")
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("panic should terminate response")
			}
			select {
			case msg := <-logs:
				if !strings.Contains(msg, "hook panic") {
					t.Fatal(msg)
				}
			case <-time.After(time.Second):
				t.Fatal("missing error log")
			}
		})
	}
}

func TestServerTLSCertificateSources(t *testing.T) {
	if _, err := NewWithOptions("127.0.0.1:0", nil, Options{TLSConfig: &tls.Config{}}); err == nil {
		t.Fatal("missing certificate source accepted")
	}
	for _, config := range []*tls.Config{
		{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }},
		{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil }},
	} {
		if _, err := NewWithOptions("127.0.0.1:0", nil, Options{TLSConfig: config}); err != nil {
			t.Fatal(err)
		}
	}
}
