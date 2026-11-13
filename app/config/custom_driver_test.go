package config

import "testing"

func TestDatabaseDriverAndURLRemainBusinessOwned(t *testing.T) {
	const driver = "business-custom"
	const dsn = "custom://service/database?option=business"
	cfg, err := LoadFrom(func(key string) (string, bool) {
		switch key {
		case "DATABASE_DRIVER":
			return driver, true
		case "DATABASE_URL":
			return dsn, true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Driver != driver || cfg.Database.URL != dsn {
		t.Fatal("configuration must preserve the business driver and DSN")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
