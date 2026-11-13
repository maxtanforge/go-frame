// Package startup owns process loading diagnostics and the optional logger runtime.
package startup

import (
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// LogSettings contains only the deployment components required by logging.
type LogSettings struct {
	Service appconfig.ServiceConfig
	Log     appconfig.LogConfig
}

// LoggerOptions validates and maps logging settings without opening files.
// File output is always isolated by instance. The operator owns the log root.
func LoggerOptions(c LogSettings) (logger.RuntimeOptions, error) {
	if err := c.Service.Validate(); err != nil {
		return logger.RuntimeOptions{}, config.PrefixValidation("service", err)
	}
	if err := c.Log.Validate(); err != nil {
		return logger.RuntimeOptions{}, config.PrefixValidation("log", err)
	}
	opts := logger.RuntimeOptions{Options: logger.Options{
		Format: c.Log.Format, Level: c.Log.Level, AddSource: c.Log.Format == "json",
		Environment: c.Service.Env, Timezone: c.Log.Timezone,
	}}
	if c.Log.Output == "stdout" {
		return opts, nil
	}
	m, err := c.Service.Metadata()
	if err != nil {
		return logger.RuntimeOptions{}, err
	}
	path, err := m.LogPath(c.Log.Dir)
	if err != nil {
		return logger.RuntimeOptions{}, err
	}
	opts.Stdout = c.Log.Output == "both"
	opts.File = &logger.FileOptions{
		Path: path, MaxSizeMB: c.Log.MaxSizeMB, MaxBackups: &c.Log.MaxBackups,
		MaxAgeDays: &c.Log.MaxAgeDays, Compress: &c.Log.Compress, Timezone: c.Log.Timezone,
	}
	return opts, nil
}
