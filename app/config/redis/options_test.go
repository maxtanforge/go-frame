package redis_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	adapter "github.com/maxtanforge/go-frame/app/config/redis"
	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	c := appconfig.Defaults().Redis
	c.Addr = "127.0.0.1:1"
	c.Username = "user"
	c.Password = "secret"
	c.DB = 3
	c.TLSEnabled = true
	c.TLSServerName = "cache.internal"
	o, err := adapter.Options(c)
	require.NoError(t, err)
	require.Equal(t, c.Username, o.Username)
	require.Equal(t, 3, o.DB)
	require.NotNil(t, o.TLSConfig)
	require.False(t, o.TLSConfig.InsecureSkipVerify)
	require.Equal(t, uint16(tls.VersionTLS12), o.TLSConfig.MinVersion)
	require.Equal(t, "cache.internal", o.TLSConfig.ServerName)
	require.Equal(t, c.PoolSize, o.PoolSize)
	c.TLSCAFile = filepath.Join(t.TempDir(), "secret-ca.pem")
	require.NoError(t, os.WriteFile(c.TLSCAFile, []byte("not a certificate"), 0600))
	_, err = adapter.Options(c)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-ca")
	c.TLSCAFile = ""
	c.TLSCertFile = "secret-cert"
	c.TLSKeyFile = "secret-key"
	_, err = adapter.Options(c)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-")
}

func TestValidCAAndClientCertificate(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, DNSNames: []string{"redis.internal"}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600))
	c := appconfig.Defaults().Redis
	c.TLSEnabled = true
	c.TLSCAFile = certPath
	c.TLSCertFile = certPath
	c.TLSKeyFile = keyPath
	c.TLSServerName = "redis.internal"
	o, err := adapter.Options(c)
	require.NoError(t, err)
	require.Len(t, o.TLSConfig.Certificates, 1)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	_, err = cert.Verify(x509.VerifyOptions{Roots: o.TLSConfig.RootCAs, DNSName: "redis.internal"})
	require.NoError(t, err)
	_, err = cert.Verify(x509.VerifyOptions{Roots: o.TLSConfig.RootCAs, DNSName: "wrong.internal"})
	require.Error(t, err)
	peer := make(chan bool, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer <- len(r.TLS.VerifiedChains) > 0
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: o.TLSConfig.Certificates, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: o.TLSConfig.RootCAs}
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{TLSClientConfig: o.TLSConfig}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	select {
	case verified := <-peer:
		require.True(t, verified)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not verify client certificate")
	}
}
