// Package identity supplies deployment-independent service and instance metadata.
package identity

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
)

// Metadata is a snapshot suitable for all loggers in one service instance.
// InstanceID is not an authentication identity or a Snowflake node number.
type Metadata struct {
	Service     string
	Version     string
	Environment string
	InstanceID  string
}

// Options is the constructor form of Metadata.
type Options Metadata

// All automatically configured components in one process share an ID. Explicit
// IDs must be unique among replicas; reusing one also reuses its log directory.
var processID = rand.Text()

// New validates required fields and fills defaults; see Metadata for semantics.
func New(opts Options) (Metadata, error) {
	if strings.TrimSpace(opts.Service) == "" || strings.TrimSpace(opts.Environment) == "" {
		return Metadata{}, errors.New("identity: service and environment are required")
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.InstanceID == "" {
		opts.InstanceID = processID
	}
	if err := ValidateInstanceID(opts.InstanceID); err != nil {
		return Metadata{}, err
	}
	return Metadata(opts), nil
}

// ValidateInstanceID restricts IDs to portable single directory names: 1–128
// ASCII letters, digits, '_' or '-', starting with a letter or digit. Windows
// reserved device names are rejected on all platforms. Errors do not echo input.
func ValidateInstanceID(id string) error {
	invalid := errors.New("identity: invalid instance ID")
	if len(id) == 0 || len(id) > 128 {
		return invalid
	}
	for i, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '_' || c == '-') {
			continue
		}
		return invalid
	}
	upper := strings.ToUpper(id)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return invalid
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		return invalid
	}
	return nil
}

// Attrs renders the snapshot as stable slog attributes.
func (m Metadata) Attrs() []slog.Attr {
	return []slog.Attr{
		slog.String("service", m.Service), slog.String("version", m.Version),
		slog.String("environment", m.Environment), slog.String("instance_id", m.InstanceID),
	}
}

// LogPath returns an absolute, lexically confined instance path. The directory
// root is trusted operator configuration; its contents and symlinks must not be
// writable by untrusted users. This function performs no filesystem writes.
func (m Metadata) LogPath(root string) (string, error) {
	if err := ValidateInstanceID(m.InstanceID); err != nil {
		return "", err
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("identity: log directory is required")
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, m.InstanceID, "service.log"), nil
}
