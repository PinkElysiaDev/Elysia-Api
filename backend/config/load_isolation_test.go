package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadKeepsConfigurationsIndependent(t *testing.T) {
	// The executable publishes its configuration once. Concurrent loaders must
	// neither overwrite that pointer nor share mutable configuration instances.
	previous := GlobalConfig
	running := &Config{Host: "running"}
	GlobalConfig = running
	t.Cleanup(func() { GlobalConfig = previous })
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"loaded"}`), 0600); err != nil {
		t.Fatal(err)
	}
	const count = 8
	configs := make([]*Config, count)
	errors := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			configs[i], errors[i] = Load(path)
		}()
	}
	wg.Wait()
	if GlobalConfig != running {
		t.Fatal("loading a configuration replaced the running configuration")
	}
	seen := make(map[*Config]bool)
	for i, cfg := range configs {
		if errors[i] != nil {
			t.Fatal(errors[i])
		}
		if cfg == nil || seen[cfg] || cfg.Host != "loaded" {
			t.Fatalf("load %d did not return an independent configuration: %+v", i, cfg)
		}
		seen[cfg] = true
	}
}
