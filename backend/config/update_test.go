package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConfigUpdatePreservesUnknownSectionFields(t *testing.T) {
	const original = `{"logLifecycleVersion":1,"httpTimeout":5,
		"agent":{"toolLoopLimit":12,"vendor":{"n":9007199254740993}},
		"agentRemote":{"publicUrl":"https://old.example","vendor":9007199254740993},
		"modelCatalog":{"url":"https://old.example/catalog","vendor":9007199254740993},
		"systemLog":{"maxRecords":100,"vendor":9007199254740993},
		"usageLog":{"bodyMaxKB":32,"vendor":9007199254740993},
		"outbound":{"deniedIpRanges":["127.0.0.0/8"],"vendor":9007199254740993}}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, shouldClearSettings := range []bool{false, true} {
		if err := cfg.Update(func(candidate *Config) error {
			candidate.SetHTTPTimeout(30)
			if shouldClearSettings {
				candidate.Agent = AgentDefaultsConfig{}
				candidate.AgentRemote = AgentRemoteConfig{}
				candidate.ModelCatalog = ModelCatalogConfig{}
				candidate.SystemLog = LogRetentionConfig{}
				candidate.UsageLog = UsageLogConfig{}
				candidate.SetOutboundDeniedIPRanges([]string{})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		saved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var sections map[string]json.RawMessage
		if err := json.Unmarshal(saved, &sections); err != nil {
			t.Fatal(err)
		}
		for section, managedField := range map[string]string{
			"agent": "toolLoopLimit", "agentRemote": "publicUrl", "modelCatalog": "url",
			"systemLog": "maxRecords", "usageLog": "bodyMaxKB", "outbound": "deniedIpRanges",
		} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(sections[section], &fields); err != nil {
				t.Fatalf("%s: %v", section, err)
			}
			var vendor bytes.Buffer
			if err := json.Compact(&vendor, fields["vendor"]); err != nil {
				t.Fatalf("%s lost vendor field: %v", section, err)
			}
			wantVendor := "9007199254740993"
			if section == "agent" {
				wantVendor = `{"n":9007199254740993}`
			}
			if vendor.String() != wantVendor {
				t.Fatalf("%s changed vendor value: %s", section, vendor.String())
			}
			if shouldClearSettings && section != "outbound" && fields[managedField] != nil {
				t.Fatalf("%s retained cleared field %s", section, managedField)
			}
			if shouldClearSettings && section == "outbound" && string(fields[managedField]) != "[]" {
				t.Fatalf("explicit empty denylist lost: %s", fields[managedField])
			}
		}
		if err := cfg.Reload(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigUpdateRejectsInvalidStoredSection(t *testing.T) {
	cfg, path := loadUpdateConfig(t)
	for _, section := range []string{"agent", "agentRemote", "modelCatalog", "usageLog", "systemLog", "outbound"} {
		invalid := []byte(`{"logLifecycleVersion":1,"` + section + `":42}`)
		if err := os.WriteFile(path, invalid, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Update(func(candidate *Config) error { candidate.SetHTTPTimeout(90); return nil }); err == nil {
			t.Fatalf("overwrote invalid %s section", section)
		}
		saved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(saved, invalid) || cfg.GetHTTPTimeout() != 0 {
			t.Fatalf("failed %s update changed file or memory", section)
		}
	}
}

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
