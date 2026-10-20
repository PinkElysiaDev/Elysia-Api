package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if limits := cfg.GetPreparationLimits(); !limits.Enabled || limits.MaxConcurrent != 8 || limits.MaxQueued != 32 || limits.MaxReservedBodyBytes != 64<<20 || limits.QueueTimeout != 5*time.Second {
		t.Fatal(limits)
	}
	zero, off := 0, false
	cfg.SetPreparation(PreparationConfig{Enabled: &off, MaxConcurrent: 1, MaxQueued: &zero, MaxReservedBodyBytes: 128 << 20, QueueTimeoutMillis: 250})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if limits := loaded.GetPreparationLimits(); limits.Enabled || limits.MaxConcurrent != 1 || limits.MaxQueued != 0 || limits.MaxReservedBodyBytes != 128<<20 || limits.QueueTimeout != 250*time.Millisecond {
		t.Fatal(limits)
	}
	for _, invalid := range []string{`{"maxConcurrent":-1}`, `{"maxQueued":-1}`, `{"maxReservedBodyBytes":-1}`, `{"queueTimeoutMillis":600001}`} {
		if err := os.WriteFile(path, []byte(`{"preparation":`+invalid+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid policy accepted", invalid)
		}
		before := loaded.GetPreparationLimits()
		if err := loaded.Reload(); err == nil || loaded.GetPreparationLimits() != before {
			t.Fatal("invalid reload published")
		}
	}
	if err := os.WriteFile(path, []byte(`{"preparation":{"maxConcurrent":2,"futureOption":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"futureOption"`) {
		t.Fatal("unknown preparation setting lost")
	}
	before := loaded.GetPreparationLimits()
	if err := loaded.Update(func(candidate *Config) error {
		candidate.SetPreparation(PreparationConfig{MaxConcurrent: -1})
		return nil
	}); err == nil || loaded.GetPreparationLimits() != before {
		t.Fatal("invalid transactional update published")
	}
}
