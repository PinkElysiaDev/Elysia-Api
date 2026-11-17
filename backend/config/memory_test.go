package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryLimitConfigValidationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write := func(raw string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"memoryLimitBytes":-1}`)
	if _, err := Load(path); err == nil {
		t.Fatal("negative memory limit accepted")
	}
	write(`{"memoryLimitBytes":123456789}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MemoryLimitBytes != 123456789 {
		t.Fatal("explicit memory limit clamped", cfg.MemoryLimitBytes)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.MemoryLimitBytes != 123456789 {
		t.Fatal("save lost limit", err)
	}
	write(`{"memoryLimitBytes":-2}`)
	if err := cfg.Reload(); err == nil || cfg.MemoryLimitBytes != 123456789 {
		t.Fatal("invalid reload changed limit", err)
	}
	write(`{"memoryLimitBytes":0}`)
	if err := cfg.Reload(); err != nil || cfg.MemoryLimitBytes != 0 {
		t.Fatal("zero did not restore automatic policy", err)
	}
}
