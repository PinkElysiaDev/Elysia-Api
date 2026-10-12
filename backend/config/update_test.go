package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func loadUpdateConfig(t *testing.T) (*Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"logLifecycleVersion":1,"agent":{"toolLoopLimit":12},"vendor":{"n":9007199254740993},"outbound":{"deniedIpRanges":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, path
}

func TestAgentSettingsSaveAndReload(t *testing.T) {
	cfg, path := loadUpdateConfig(t)
	cfg.SetAgentToolLoopLimit(17)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ResolveAgentToolLoopLimit() != 17 {
		t.Fatal("saved Agent setting lost")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("unknown configuration was rounded")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "17", "19", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Reload(); err != nil {
		t.Fatal(err)
	}
	if cfg.ResolveAgentToolLoopLimit() != 19 {
		t.Fatal("Reload ignored Agent configuration")
	}
}

func TestConfigUpdateFailureDoesNotPublish(t *testing.T) {
	cfg, path := loadUpdateConfig(t)
	before, _ := os.ReadFile(path)
	if err := cfg.Update(func(candidate *Config) error {
		candidate.SetAgentToolLoopLimit(70)
		return errors.New("preparation failed")
	}); err == nil {
		t.Fatal("expected preparation failure")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || cfg.ResolveAgentToolLoopLimit() != 12 {
		t.Fatal("failed preparation mutated configuration")
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(candidate *Config) error { candidate.SetAgentToolLoopLimit(80); return nil }); err == nil {
		t.Fatal("expected storage failure")
	}
	if cfg.ResolveAgentToolLoopLimit() != 12 {
		t.Fatal("failed save published candidate")
	}
}

func TestConfigUpdateSerializesAndPreservesRuntimeState(t *testing.T) {
	cfg, path := loadUpdateConfig(t)
	cfg.Tokens = []AccessToken{{Name: "runtime"}}
	cfg.Groups = []ModelGroupConfig{{ID: "runtime"}}
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			if err := cfg.Update(func(candidate *Config) error {
				candidate.SetHTTPTimeout(candidate.GetHTTPTimeout() + 1)
				candidate.SetAgentToolLoopLimit(candidate.GetAgentDefaults().ToolLoopLimit + 1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if cfg.GetHTTPTimeout() != 10 || cfg.ResolveAgentToolLoopLimit() != 22 {
		t.Fatal("concurrent update was lost")
	}
	if len(cfg.GetTokens()) != 1 || len(cfg.GetGroups()) != 1 {
		t.Fatal("runtime identities changed")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetHTTPTimeout() != 10 || loaded.ResolveAgentToolLoopLimit() != 22 || loaded.GetOutboundConfig().DeniedIPRanges == nil || len(loaded.GetOutboundConfig().DeniedIPRanges) != 0 {
		t.Fatal("persisted state differs")
	}
}
