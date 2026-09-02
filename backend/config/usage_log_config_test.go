package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// 回归：SetHost/SetPort 后 Save() 必须把新值落盘（此前设置页的 host/port
// 从未被应用，保存等于白保存）；modelCatalog 块同样要随 Save 持久化。
func TestSavePersistsHostPortAndModelCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":8765}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	cfg.SetHost("0.0.0.0")
	cfg.SetPort(9000)
	interval := 60
	cfg.SetModelCatalogSyncInterval(interval)
	if cfg.Host != "0.0.0.0" || cfg.Server.Host != "0.0.0.0" || cfg.Port != 9000 || cfg.Server.Port != 9000 {
		t.Fatalf("SetHost/SetPort must sync nested Server fields: %+v", cfg.Server)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["host"] != "0.0.0.0" || persisted["port"] != float64(9000) {
		t.Fatalf("host/port must persist: %s", raw)
	}
	catalog, ok := persisted["modelCatalog"].(map[string]any)
	if !ok || catalog["syncIntervalMinutes"] != float64(60) {
		t.Fatalf("modelCatalog.syncIntervalMinutes must persist: %s", raw)
	}

	// Reload 后新值生效（含 ModelCatalog 拷贝——此前 Reload 漏拷该字段）。
	cfg2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg2.GetModelCatalog(); got.SyncIntervalMinutes == nil || *got.SyncIntervalMinutes != 60 {
		t.Fatalf("reloaded catalog interval = %+v, want 60", got.SyncIntervalMinutes)
	}
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","modelCatalog":{"syncIntervalMinutes":30}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg2.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cfg2.GetModelCatalog(); got.SyncIntervalMinutes == nil || *got.SyncIntervalMinutes != 30 {
		t.Fatalf("Reload must refresh ModelCatalog, got %+v", got)
	}
	if cfg2.Host != "127.0.0.1" {
		t.Fatalf("Reload must refresh host, got %q", cfg2.Host)
	}
}

// 回归（-race）：Reload 全程持锁后，与「setter + Save」并发不再丢失更新，
// 且热路径访问器（GetMaxBodyBytes）与 Reload 无数据竞争。旧实现 Reload
// 在锁外读文件，交错时会把刚落盘的值用旧文件内容覆盖。
func TestReloadSaveConcurrentNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"logLevel":"info"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; i < 300; i++ {
			cfg.SetLogLevel("debug")
			if err := cfg.Save(); err != nil {
				t.Errorf("save: %v", err)
				return
			}
			_ = cfg.GetMaxBodyBytes()
		}
	}()
	// 并发 Reload：旧实现读文件在锁外，与上面的 Save 交错即构成丢失更新
	//（读旧文件 → 等锁 → 用旧值覆盖内存 → 下次 Save 把旧值写回磁盘）。
reloadLoop:
	for {
		if err := cfg.Reload(); err != nil {
			t.Errorf("reload: %v", err)
			return
		}
		select {
		case <-writerDone:
			// writer 已结束：最后 Reload 一次读到最终文件内容再收敛。
			if err := cfg.Reload(); err != nil {
				t.Fatalf("final reload: %v", err)
			}
			break reloadLoop
		default:
		}
	}
	<-writerDone

	// 收敛断言：再 Save 一次后，磁盘与内存必须一致（丢失更新会让内存
	// 停留在旧值，随后把旧值写回磁盘）。
	cfg.SetLogLevel("warn")
	if err := cfg.Save(); err != nil {
		t.Fatalf("final save: %v", err)
	}
	if got := cfg.GetLogLevel(); got != "warn" {
		t.Fatalf("in-memory logLevel = %q, want warn", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		LogLevel string `json:"logLevel"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.LogLevel != "warn" {
		t.Fatalf("on-disk logLevel = %q, want warn (lost update)", onDisk.LogLevel)
	}
}
