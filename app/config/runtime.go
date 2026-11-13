package config

import "github.com/maxtanforge/go-frame/identity"

// Metadata resolves deployment-neutral log attributes without opening resources.
func (c ServiceConfig) Metadata() (identity.Metadata, error) {
	return identity.New(identity.Options{Service: c.Name, Version: c.Version, Environment: c.Env, InstanceID: c.InstanceID})
}
