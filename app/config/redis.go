package config

import (
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/maxtanforge/go-frame/config"
)

// Validate checks Redis settings without opening files or network connections.
func (c RedisConfig) Validate() error {
	if c.Addr != "" {
		host, port, err := net.SplitHostPort(c.Addr)
		if err != nil || strings.TrimSpace(host) == "" {
			return config.Invalid("addr", config.RuleFormat)
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return config.Invalid("addr", config.RuleRange)
		}
	}
	if c.DB < 0 || c.DB > math.MaxInt32 {
		return config.Invalid("db", config.RuleRange)
	}
	if c.PoolSize <= 0 || int64(c.PoolSize) > math.MaxInt32 {
		return config.Invalid("pool_size", config.RuleRange)
	}
	if c.MaxRetries < 0 {
		return config.Invalid("max_retries", config.RuleRange)
	}
	for _, v := range []struct {
		path  string
		value time.Duration
	}{
		{"dial_timeout", c.DialTimeout}, {"read_timeout", c.ReadTimeout}, {"write_timeout", c.WriteTimeout}, {"pool_timeout", c.PoolTimeout},
	} {
		if v.value <= 0 {
			return config.Invalid(v.path, config.RuleRange)
		}
	}
	if !c.TLSEnabled && (c.TLSCAFile != "" || c.TLSCertFile != "" || c.TLSKeyFile != "" || c.TLSServerName != "") {
		return config.Invalid("tls_enabled", config.RuleRelation)
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		path := "tls_key_file"
		if c.TLSCertFile == "" {
			path = "tls_cert_file"
		}
		return config.Invalid(path, config.RuleRelation)
	}
	return nil
}
