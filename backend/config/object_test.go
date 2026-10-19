package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRequiresJSONObject(t *testing.T) {
	for _, content := range []string{"null", "[]", "false", "123"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("non-object configuration accepted")
			}
			cfg := &Config{path: path, Host: "127.0.0.1", Port: 8765}
			if err := cfg.Reload(); err == nil || cfg.Port != 8765 {
				t.Fatal("invalid reload changed running configuration", err)
			}
		})
	}
}
