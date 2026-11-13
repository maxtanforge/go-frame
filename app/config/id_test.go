package config

import "testing"

func TestSnowflakeNodeConfiguration(t *testing.T) {
	if Defaults().ID.Node != 0 {
		t.Fatal("default node must be 0")
	}
	for _, node := range []int64{-2, -1, 0, 1023, 1024} {
		cfg := Defaults()
		cfg.ID.Node = node
		err := cfg.Validate()
		if (err == nil) != (node >= 0 && node <= 1023) {
			t.Fatalf("node %d: %v", node, err)
		}
	}
	cfg, err := LoadFrom(func(key string) (string, bool) { return "42", key == "ID_NODE" })
	if err != nil || cfg.ID.Node != 42 {
		t.Fatal(cfg.ID, err)
	}
}
