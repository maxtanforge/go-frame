package identity

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetadataIsStableAndInstancePathIsIsolated(t *testing.T) {
	a, err := New(Options{Service: "orders", Version: "1.2", Environment: "prod", InstanceID: "node-A"})
	require.NoError(t, err)
	b, err := New(Options{Service: "orders", Version: "1.2", Environment: "prod", InstanceID: "node-B"})
	require.NoError(t, err)
	root := t.TempDir()
	p, err := a.LogPath(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "node-A", "service.log"), p)
	q, err := b.LogPath(root)
	require.NoError(t, err)
	require.NotEqual(t, p, q)
	attrs := map[string]string{}
	for _, a := range a.Attrs() {
		attrs[a.Key] = a.Value.String()
	}
	require.Equal(t, "orders", attrs["service"])
	require.Equal(t, "1.2", attrs["version"])
	require.Equal(t, "prod", attrs["environment"])
	require.Equal(t, "node-A", attrs["instance_id"])
}

func TestGeneratedIdentityIsProcessScoped(t *testing.T) {
	a, err := New(Options{Service: "orders", Environment: "dev"})
	require.NoError(t, err)
	b, err := New(Options{Service: "orders", Environment: "dev"})
	require.NoError(t, err)
	require.NotEmpty(t, a.InstanceID)
	require.Equal(t, a.InstanceID, b.InstanceID)
	require.NoError(t, ValidateInstanceID(a.InstanceID))
}

func TestRejectUnsafeInstanceNames(t *testing.T) {
	for _, id := range []string{".", "..", "../sibling", `..\sibling`, "/tmp", "a:b", "x.y", " space", "CON", "nul", "LPT1", "com9", "aux", "-leading"} {
		t.Run(id, func(t *testing.T) {
			_, err := New(Options{Service: "orders", Environment: "prod", InstanceID: id})
			require.Error(t, err)
			_, err = (Metadata{InstanceID: id}).LogPath(t.TempDir())
			require.Error(t, err)
		})
	}
	_, err := New(Options{})
	require.Error(t, err)
	_, err = (Metadata{InstanceID: "safe"}).LogPath("")
	require.Error(t, err)
}
