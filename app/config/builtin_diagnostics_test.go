package config

import (
	"errors"
	"strconv"
	"testing"

	"github.com/maxtanforge/go-frame/config"
)

func TestBuiltinValidationDiagnostics(t *testing.T) {
	cases := []struct {
		name, path, env, value string
		rule                   config.Rule
	}{
		{"environment", "service.env", "APP_ENV", "invalid", config.RuleChoice},
		{"name", "service.name", "APP_NAME", " ", config.RuleRequired},
		{"instance", "service.instance_id", "INSTANCE_ID", "invalid/name", config.RuleFormat},
		{"level", "log.level", "LOG_LEVEL", "invalid", config.RuleChoice},
		{"port format", "http.port", "HTTP_PORT", "abc", config.RuleFormat},
		{"port range", "http.port", "HTTP_PORT", "65536", config.RuleRange},
		{"http timeout", "http.timeout", "HTTP_TIMEOUT", "0s", config.RuleRange},
		{"shutdown timeout", "lifecycle.shutdown_timeout", "SHUTDOWN_TIMEOUT", "0s", config.RuleRange},
		{"open connections", "database.max_open_conns", "DB_MAX_OPEN_CONNS", "0", config.RuleRange},
		{"idle connections", "database.max_idle_conns", "DB_MAX_IDLE_CONNS", "0", config.RuleRange},
		{"driver", "database.driver", "DATABASE_DRIVER", " ", config.RuleRequired},
		{"connection relation", "database.max_idle_conns", "DB_MAX_IDLE_CONNS", "26", config.RuleRelation},
		{"redis retries", "redis.max_retries", "REDIS_MAX_RETRIES", "-1", config.RuleRange},
		{"redis address", "redis.addr", "REDIS_ADDR", "invalid", config.RuleFormat},
		{"redis port", "redis.addr", "REDIS_ADDR", "localhost:0", config.RuleRange},
		{"redis database", "redis.db", "REDIS_DB", "-1", config.RuleRange},
		{"redis pool", "redis.pool_size", "REDIS_POOL_SIZE", "0", config.RuleRange},
		{"redis dial", "redis.dial_timeout", "REDIS_DIAL_TIMEOUT", "0s", config.RuleRange},
		{"redis read", "redis.read_timeout", "REDIS_READ_TIMEOUT", "0s", config.RuleRange},
		{"redis write", "redis.write_timeout", "REDIS_WRITE_TIMEOUT", "0s", config.RuleRange},
		{"redis pool timeout", "redis.pool_timeout", "REDIS_POOL_TIMEOUT", "0s", config.RuleRange},
		{"connection idle", "database.conn_max_idle_time", "DB_CONN_MAX_IDLE_TIME", "0s", config.RuleRange},
		{"connection lifetime", "database.conn_max_lifetime", "DB_CONN_MAX_LIFETIME", "0s", config.RuleRange},
		{"startup", "lifecycle.startup_timeout", "STARTUP_TIMEOUT", "0s", config.RuleRange},
		{"output", "log.output", "LOG_OUTPUT", "invalid", config.RuleChoice},
		{"directory", "log.dir", "LOG_DIR", " ", config.RuleRequired},
		{"log size", "log.max_size_mb", "LOG_MAX_SIZE_MB", "0", config.RuleRange},
		{"log backups", "log.max_backups", "LOG_MAX_BACKUPS", "-1", config.RuleRange},
		{"log age", "log.max_age_days", "LOG_MAX_AGE_DAYS", "-1", config.RuleRange},
		{"timezone", "log.timezone", "LOG_TIMEZONE", "invalid/timezone", config.RuleFormat},
		{"shutdown delay", "lifecycle.shutdown_delay", "SHUTDOWN_DELAY", "10s", config.RuleRelation},
		{"cleanup reserve", "lifecycle.cleanup_reserve", "SHUTDOWN_CLEANUP_RESERVE", "10s", config.RuleRelation},
	}
	if strconv.IntSize == 64 {
		cases = append(cases, struct {
			name, path, env, value string
			rule                   config.Rule
		}{"log size overflow", "log.max_size_mb", "LOG_MAX_SIZE_MB", "8796093022208", config.RuleRange})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Decode(Defaults(), config.Source{Name: "deployment", Values: map[string]string{tc.path: tc.value}})
			assertBuiltinDiagnostic(t, err, tc.path, tc.env, "deployment", tc.rule)
		})
	}
}

func TestBuiltinRedisTLSRelationDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    map[string]string
		path, env string
	}{
		{"disabled", map[string]string{"redis.tls_ca_file": "ca.pem"}, "redis.tls_enabled", "REDIS_TLS_ENABLED"},
		{"missing key", map[string]string{"redis.tls_enabled": "true", "redis.tls_cert_file": "cert.pem"}, "redis.tls_key_file", "REDIS_TLS_KEY_FILE"},
		{"missing cert", map[string]string{"redis.tls_enabled": "true", "redis.tls_key_file": "key.pem"}, "redis.tls_cert_file", "REDIS_TLS_CERT_FILE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Decode(Defaults(), config.Source{Name: "deployment", Values: tc.values})
			assertBuiltinDiagnostic(t, err, tc.path, tc.env, "defaults", config.RuleRelation)
		})
	}
}

func TestBuiltinRedisStandaloneDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		path, env, value string
		rule             config.Rule
	}{
		{"addr", "REDIS_ADDR", "localhost:0", config.RuleRange},
		{"pool_size", "REDIS_POOL_SIZE", "0", config.RuleRange},
		{"max_retries", "REDIS_MAX_RETRIES", "-1", config.RuleRange},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, err := config.Decode(Defaults().Redis, config.Source{Name: "deployment", Values: map[string]string{tc.path: tc.value}})
			assertBuiltinDiagnostic(t, err, tc.path, tc.env, "deployment", tc.rule)
		})
	}
}

func assertBuiltinDiagnostic(t *testing.T, err error, path, env, source string, rule config.Rule) {
	t.Helper()
	var detail *config.FieldError
	if !errors.As(err, &detail) {
		t.Fatalf("expected config.FieldError, got %T: %v", err, err)
	}
	if detail.Path != path || detail.Env != env || detail.Source != source || detail.Rule != rule {
		t.Fatalf("unexpected diagnostic: %+v; want path=%s env=%s source=%s rule=%s", detail, path, env, source, rule)
	}
}
