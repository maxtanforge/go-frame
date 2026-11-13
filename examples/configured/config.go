package main

import (
	"net"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/config/loader"
)

// Business owns its configuration; adding a field requires no framework change.
type settings struct {
	Framework appconfig.Config `config:"framework"`
	Host      string           `config:"host" env:"LISTEN_HOST"`
	Business  businessSettings `config:"business" envPrefix:"BUSINESS_"`
}
type businessSettings struct {
	Message string   `config:"message" env:"MESSAGE"`
	Labels  []string `config:"labels" env:"LABELS"`
	APIKey  string   `config:"api_key" env:"API_KEY" secret:"true"`
}

func (c settings) Validate() error {
	if err := c.Framework.Validate(); err != nil {
		return config.PrefixValidation("framework", err)
	}
	if net.ParseIP(c.Host) == nil {
		return config.Invalid("host", config.RuleFormat)
	}
	if len(c.Business.Message) > 1024 {
		return config.Invalid("business.message", config.RuleLength)
	}
	return nil
}

// Business policy stays in the example; the optional loader owns source parsing.
type settingsInputs struct {
	*loader.Inputs[settings]
	watch bool
}

func newInputs(args []string, lookup func(string) (string, bool)) (*settingsInputs, error) {
	defaults := settings{Framework: appconfig.Defaults(), Host: "127.0.0.1", Business: businessSettings{Message: "hello", Labels: []string{}}}
	defaults.Framework.Service.Name = "configured"
	in, err := loader.New(defaults, args, loader.Options[settings]{
		Lookup: lookup, AllowWatch: true,
		AllowDotenv: func(c settings) bool { return c.Framework.Service.Env == "dev" },
	})
	if err != nil {
		return nil, err
	}
	return &settingsInputs{Inputs: in, watch: in.Watch()}, nil
}

func loadSettings(args []string, lookup func(string) (string, bool)) (config.Result[settings], error) {
	in, err := newInputs(args, lookup)
	if err != nil {
		return config.Result[settings]{}, err
	}
	return in.Load()
}
