package bootstrap

import (
	"net"
	"strings"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/config/loader"
	"github.com/maxtanforge/go-frame/http/request"
)

type settings struct {
	Framework      runtimeSettings  `config:"framework"`
	Host           string           `config:"host" env:"LISTEN_HOST"`
	Greeting       greetingSettings `config:"greeting"`
	TrustedProxies string           `config:"trusted_proxies" env:"TRUSTED_PROXIES"`
	MetricsEnabled bool             `config:"metrics_enabled" env:"METRICS_ENABLED"`
}

// runtimeSettings belongs to this service: it chooses the components to deploy.
// Optional providers are validated only when their endpoint is configured.
type runtimeSettings struct {
	Service   appconfig.ServiceConfig
	HTTP      appconfig.HTTPConfig
	Lifecycle appconfig.LifecycleConfig
	Log       appconfig.LogConfig
	ID        appconfig.IDConfig
	Database  appconfig.DatabaseConfig
	Redis     appconfig.RedisConfig
}

func defaultRuntimeSettings() runtimeSettings {
	d := appconfig.Defaults()
	return runtimeSettings{Service: d.Service, HTTP: d.HTTP, Lifecycle: d.Lifecycle, Log: d.Log, ID: d.ID, Database: d.Database, Redis: d.Redis}
}

func (s runtimeSettings) Validate() error {
	for _, component := range []struct {
		path     string
		validate func() error
	}{
		{"service", s.Service.Validate}, {"http", s.HTTP.Validate},
		{"lifecycle", s.Lifecycle.Validate}, {"log", s.Log.Validate}, {"id", s.ID.Validate},
	} {
		if err := component.validate(); err != nil {
			return config.PrefixValidation(component.path, err)
		}
	}
	if s.Database.URL != "" {
		if err := s.Database.Validate(); err != nil {
			return config.PrefixValidation("database", err)
		}
	}
	if s.Redis.Addr != "" {
		if err := s.Redis.Validate(); err != nil {
			return config.PrefixValidation("redis", err)
		}
	}
	return nil
}

type greetingSettings struct {
	Prefix string `config:"prefix" env:"GREETING_PREFIX"`
}

func (s settings) Validate() error {
	if err := s.Framework.Validate(); err != nil {
		return config.PrefixValidation("framework", err)
	}
	if net.ParseIP(s.Host) == nil {
		return config.Invalid("host", config.RuleFormat)
	}
	if _, err := request.NewClientIPResolver(s.TrustedProxies); err != nil {
		return config.Invalid("trusted_proxies", config.RuleFormat)
	}
	if strings.TrimSpace(s.Greeting.Prefix) == "" || len(s.Greeting.Prefix) > 128 {
		return config.Invalid("greeting.prefix", config.RuleLength)
	}
	return nil
}

func loadSettings(args []string, lookup func(string) (string, bool)) (config.Result[settings], error) {
	defaults := settings{Framework: defaultRuntimeSettings(), Host: "127.0.0.1", Greeting: greetingSettings{Prefix: "Hello"}}
	defaults.Framework.Service.Name = "service"
	inputs, err := loader.New(defaults, args, loader.Options[settings]{Lookup: lookup, AllowDotenv: func(s settings) bool { return s.Framework.Service.Env == "dev" }})
	if err != nil {
		return config.Result[settings]{}, err
	}
	return inputs.Load()
}
