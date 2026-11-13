package config

import (
	"testing"

	"github.com/maxtanforge/go-frame/config"
	"github.com/stretchr/testify/require"
)

func TestDatabaseDriverSelection(t *testing.T) {
	for _, tc := range []struct{ value, want string }{{"mysql", "mysql"}, {"postgres", "postgres"}} {
		c, err := LoadFrom(lookup(map[string]string{"DATABASE_DRIVER": tc.value}))
		require.NoError(t, err)
		require.Equal(t, tc.want, c.Database.Driver)
		require.Empty(t, c.Database.URL)
	}
	for _, value := range []string{"", " ", "\t"} {
		_, err := LoadFrom(lookup(map[string]string{"DATABASE_DRIVER": value}))
		assertBuiltinDiagnostic(t, err, "database.driver", "DATABASE_DRIVER", "environment", config.RuleRequired)
		c := Defaults()
		c.Database.Driver = value
		require.Error(t, c.Validate())
		_, err = config.Decode(c)
		assertBuiltinDiagnostic(t, err, "database.driver", "DATABASE_DRIVER", "defaults", config.RuleRequired)
	}
	c := Defaults()
	c.Database.Driver = ""
	require.Error(t, c.Validate())
}
