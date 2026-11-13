package config

import (
	"errors"
	"os"

	"github.com/maxtanforge/go-frame/config"
)

// Load reads the process environment into Config via os.LookupEnv.
func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

// LoadFrom decodes all present environment values onto Defaults, then validates.
// Explicit empty strings override defaults; empty numbers and booleans fail.
// A nil lookup is an error. Supplied values are never included in errors.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("config: lookup function is required")
	}
	result, err := config.Decode(Defaults(), config.Source{Name: "environment", Lookup: lookup})
	if err != nil {
		return Config{}, err
	}
	return result.Value, nil
}
