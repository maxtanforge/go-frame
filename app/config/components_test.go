package config

import (
	"testing"

	"github.com/maxtanforge/go-frame/config"
	"github.com/stretchr/testify/require"
)

func TestComponentsValidateIndependently(t *testing.T) {
	c := Defaults()
	for _, component := range []interface{ Validate() error }{c.Service, c.HTTP, c.Lifecycle, c.Database, c.Redis, c.Log, c.ID} {
		require.NoError(t, component.Validate())
	}
	for _, tc := range []struct {
		name, path, env string
		rule            config.Rule
		decode          func() error
	}{
		{"service", "name", "APP_NAME", config.RuleRequired, func() error { value := c.Service; value.Name = " "; _, err := config.Decode(value); return err }},
		{"http", "timeout", "HTTP_TIMEOUT", config.RuleRange, func() error { value := c.HTTP; value.Timeout = 0; _, err := config.Decode(value); return err }},
		{"lifecycle", "cleanup_reserve", "SHUTDOWN_CLEANUP_RESERVE", config.RuleRelation, func() error {
			value := c.Lifecycle
			value.CleanupReserve = value.ShutdownTimeout
			_, err := config.Decode(value)
			return err
		}},
		{"database", "driver", "DATABASE_DRIVER", config.RuleRequired, func() error { value := c.Database; value.Driver = " "; _, err := config.Decode(value); return err }},
		{"redis", "pool_size", "REDIS_POOL_SIZE", config.RuleRange, func() error { value := c.Redis; value.PoolSize = 0; _, err := config.Decode(value); return err }},
		{"log", "format", "LOG_FORMAT", config.RuleChoice, func() error { value := c.Log; value.Format = "secret"; _, err := config.Decode(value); return err }},
		{"id", "node", "ID_NODE", config.RuleRange, func() error { value := c.ID; value.Node = -1; _, err := config.Decode(value); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBuiltinDiagnostic(t, tc.decode(), tc.path, tc.env, "defaults", tc.rule) })
	}
}

type workerConfig struct {
	Service ServiceConfig
	Log     LogConfig
}

func (c workerConfig) Validate() error {
	if err := c.Service.Validate(); err != nil {
		return config.PrefixValidation("service", err)
	}
	return config.PrefixValidation("log", c.Log.Validate())
}

func TestComposeOnlyRequiredDeploymentComponents(t *testing.T) {
	defaults := Defaults()
	worker := workerConfig{Service: defaults.Service, Log: defaults.Log}
	result, err := config.Decode(worker, config.Source{Name: "deployment", Values: map[string]string{"service.name": "worker"}})
	require.NoError(t, err)
	require.Equal(t, "worker", result.Value.Service.Name)
	_, err = config.Decode(worker, config.Source{Name: "deployment", Values: map[string]string{"log.level": "secret"}})
	assertBuiltinDiagnostic(t, err, "log.level", "LOG_LEVEL", "deployment", config.RuleChoice)
	require.NotContains(t, err.Error(), "secret")
}
