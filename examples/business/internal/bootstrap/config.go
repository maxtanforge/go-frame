// Package bootstrap owns configuration and explicit assembly for the category service.
package bootstrap

import (
	"errors"
	"net"
	"reflect"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	auth "github.com/maxtanforge/go-frame/auth/http"
)

// Settings holds validated infrastructure configuration and a borrowed verifier.
type Settings struct {
	Config   appconfig.Config
	Host     string
	Verifier auth.Verifier
}

// LoadFrom loads environment configuration without connecting to infrastructure.
func LoadFrom(lookup func(string) (string, bool)) (Settings, error) {
	cfg, err := appconfig.LoadFrom(lookup)
	if err != nil {
		return Settings{}, err
	}
	host := "127.0.0.1"
	if value, ok := lookup("LISTEN_HOST"); ok {
		host = value
	}
	verifier, err := loadVerifier(func(key string) string { value, _ := lookup(key); return value })
	if err != nil {
		return Settings{}, err
	}
	s := Settings{Config: cfg, Host: host, Verifier: verifier}
	if err := validateSettings(s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

func validateSettings(s Settings) error {
	if err := s.Config.Validate(); err != nil {
		return err
	}
	switch s.Config.Database.Driver {
	case "postgres", "mysql":
	default:
		return errors.New("business: unsupported database driver; select an adapter supported by this application")
	}
	if s.Config.Database.URL == "" {
		return errors.New("business: DATABASE_URL is required; apply the selected driver migration before startup")
	}
	if s.Config.ID.Node < 0 {
		return errors.New("business: unique ID_NODE is required")
	}
	if net.ParseIP(s.Host) == nil {
		return errors.New("business: LISTEN_HOST must be an IP address")
	}
	if s.Verifier == nil {
		return errors.New("business: verifier is required")
	}
	v := reflect.ValueOf(s.Verifier)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Func) && v.IsNil() {
		return errors.New("business: verifier is required")
	}
	return nil
}
