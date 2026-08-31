package config

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUsageLogDefaultsCleanupDisabled(t *testing.T) {
	cfg := &Config{}
	res := cfg.GetUsageLogConfig()
	if !res.PersistEnabled {
		t.Fatal("persistEnabled must default to true")
	}
	if res.RetentionDays != 0 || res.MaxStorageBytes != 0 || res.MaxRecords != 0 {
		t.Fatalf("auto cleanup must be disabled by default: %+v", res)
	}
	if res.BodyMaxBytes != DefaultUsageBodyMaxKB*1024 {
		t.Fatalf("bodyMaxBytes default = %d", res.BodyMaxBytes)
	}
	if res.ExternalizeMedia != true {
		t.Fatal("externalizeMedia must default to true")
	}
	if res.BodyOnErrorOnly {
		t.Fatal("bodyOnErrorOnly must default to false")
	}
	if res.CleanupInterval != 60*time.Minute {
		t.Fatalf("cleanup interval default = %s", res.CleanupInterval)
	}
}

func TestUsageLogLegacyFlatFallback(t *testing.T) {
	off := false
	cfg := &Config{UsagePersistEnabled: &off, UsagePersistMaxRecords: 5000}
	res := cfg.GetUsageLogConfig()
	if res.PersistEnabled {
		t.Fatal("legacy usagePersistEnabled=false must be honored")
	}
	if res.MaxRecords != 5000 {
		t.Fatalf("legacy usagePersistMaxRecords=5000 must be honored, got %d", res.MaxRecords)
	}
	// 新块显式设置后不再回退旧键。
	offNew := true
	five := 100
	cfg.SetUsageLogConfig(UsageLogConfig{PersistEnabled: &offNew, MaxRecords: &five})
	res = cfg.GetUsageLogConfig()
	if res.MaxRecords != 100 {
		t.Fatalf("explicit usageLog block must override legacy, got %d", res.MaxRecords)
	}
}

func TestUsageLogExplicitZeroSemantics(t *testing.T) {
	cfg := &Config{}
	zero := 0
	cfg.SetUsageLogConfig(UsageLogConfig{BodyMaxKB: &zero})
	res := cfg.GetUsageLogConfig()
	if res.BodyMaxBytes != 0 {
		t.Fatalf("explicit bodyMaxKB=0 means no bodies, got %d", res.BodyMaxBytes)
	}
	if cfg.GetUsageLogConfig().PersistEnabled != true {
		t.Fatal("unrelated fields must stay at defaults")
	}
}

func TestUsageLogNegativeClamped(t *testing.T) {
	cfg := &Config{}
	neg := -5
	cfg.SetUsageLogConfig(UsageLogConfig{RetentionDays: &neg, MaxStorageMB: &neg, BodyMaxKB: &neg})
	res := cfg.GetUsageLogConfig()
	if res.RetentionDays != 0 || res.MaxStorageBytes != 0 || res.BodyMaxBytes != 0 {
		t.Fatalf("negative values must clamp to 0: %+v", res)
	}
}

func TestUsageLogCleanupIntervalClamp(t *testing.T) {
	cfg := &Config{}
	one := 1
	cfg.SetUsageLogConfig(UsageLogConfig{CleanupIntervalMinutes: &one})
	if got := cfg.GetUsageLogConfig().CleanupInterval; got != 5*time.Minute {
		t.Fatalf("interval below floor must clamp to 5m, got %s", got)
	}
}

func TestUsageLogJSONRoundTripOmitsZeroBlock(t *testing.T) {
	cfg := UsageLogConfig{}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{}" {
		t.Fatalf("all-default block must marshal to {}, got %s", out)
	}
	// 指针指向 0 的字段是显式值，必须保留在 JSON 中。
	zero := 0
	cfg.RetentionDays = &zero
	out, _ = json.Marshal(cfg)
	if string(out) == "{}" {
		t.Fatal("explicit zero retentionDays must survive marshaling")
	}
}
