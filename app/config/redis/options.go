// Package redis maps configuration to Redis options, including verified TLS.
package redis

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/redis"
)

// Options validates settings and reads explicitly configured certificate files.
// It opens no network connections and never includes certificate paths in errors.
// Call during resource startup so configuration loading remains free of I/O.
func Options(c appconfig.RedisConfig) (redis.Options, error) {
	if err := c.Validate(); err != nil {
		return redis.Options{}, err
	}
	o := redis.Options{Addr: c.Addr, Username: c.Username, Password: c.Password, DB: c.DB, MaxRetries: c.MaxRetries, PoolSize: c.PoolSize, DialTimeout: c.DialTimeout, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, PoolTimeout: c.PoolTimeout}
	if !c.TLSEnabled {
		return o, nil
	}
	t := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.TLSServerName}
	if c.TLSCAFile != "" {
		data, err := os.ReadFile(c.TLSCAFile)
		if err != nil {
			return redis.Options{}, errors.New("config: cannot read REDIS_TLS_CA_FILE")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return redis.Options{}, errors.New("config: cannot load system trust roots")
		}
		if !roots.AppendCertsFromPEM(data) {
			return redis.Options{}, errors.New("config: invalid REDIS_TLS_CA_FILE")
		}
		t.RootCAs = roots
	}
	if c.TLSCertFile != "" {
		cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
		if err != nil {
			return redis.Options{}, errors.New("config: invalid REDIS_TLS_CERT_FILE or REDIS_TLS_KEY_FILE")
		}
		t.Certificates = []tls.Certificate{cert}
	}
	o.TLSConfig = t
	return o, nil
}
