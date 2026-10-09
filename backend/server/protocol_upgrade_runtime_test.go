package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestProtocolUpgradeStartupAndRestart(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	raw, err := json.Marshal(map[string]any{"databasePath": filepath.Join(directory, "gateway.sqlite"), "modelCatalog": map[string]any{"enabled": false}, "openBrowserOnStart": false})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var first *storage.ProtocolUpgradeReceipt
	for attempt := range 2 {
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		s := New(cfg)
		func() {
			defer s.doShutdown()
			defer s.store.Close()
			if s.startupErr != nil || s.protocolRuntimeError() != nil || !s.isProtocolRuntimeRequired.Load() {
				t.Fatal("production startup did not enable the verified runtime", s.startupErr, s.protocolRuntimeError())
			}
			receipt, err := s.store.ProtocolUpgradeStatus(t.Context())
			if err != nil || receipt == nil {
				t.Fatal(receipt, err)
			}
			service, err := s.protocolService()
			if err != nil || len(service.View().IDs()) != 4 {
				t.Fatal("missing shipped revisions", err)
			}
			if attempt == 0 {
				first = receipt
			} else if first.PlanHash != receipt.PlanHash || first.Backup != receipt.Backup {
				t.Fatal("restart repeated migration", first, receipt)
			}
			if receipt.Backup != "" || receipt.BackupMode != "not_required" {
				t.Fatal("empty startup requested a snapshot", receipt)
			}
			rows, err := s.store.ListCustomProtocols(t.Context())
			if err != nil || len(rows) != 0 {
				t.Fatal("empty startup seeded the legacy registry", rows, err)
			}
		}()
	}
}

func TestProtocolUpgradeRepairAPIIsIdempotentAndGatesReload(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	// 预置只读后被改的预置会自动回归 shipped 版本；真正阻断迁移的是
	// 无法自动导入的旧自定义协议（此处为带模型发现块的极简配置）。
	row := storage.CustomProtocol{ID: "legacy-custom", Name: "custom", Config: `{"id":"legacy-custom","name":"custom","models":{"path":"/models"}}`}
	if err := s.store.UpsertCustomProtocol(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	if err := s.initializeProtocolRuntime(t.Context()); err == nil || s.protocolRuntimeError() == nil {
		t.Fatal("unimportable legacy configuration did not close generation", err)
	}
	context, recorder := adminProtocolContext(http.MethodPost, "/v1/responses", `{"model":"m","input":"hi"}`)
	s.serveVersionedPublicIngress(context)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatal("failed migration fell back to legacy generation", recorder.Code, recorder.Body)
	}
	reload := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/reload", nil, "")
	if reload.Code == http.StatusOK || s.protocolRuntimeError() == nil {
		t.Fatal("reload bypassed migration", reload.Body)
	}
	forged := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/migration/apply", []byte(`{"baseline":"fake","reports":{"passed":true}}`), "")
	if forged.Code != http.StatusBadRequest {
		t.Fatal("client report accepted", forged.Code, forged.Body)
	}
	input := protocolUpgradeInput{Definitions: map[string]protocol.Value{}}
	replacement := loadGatewayDefinition(t, "text-alpha")
	replacement.ID = row.ID
	input.Definitions[row.ID] = mustEncodedProtocolValue(t, replacement)
	raw, _ := json.Marshal(input)
	preview := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/migration/preview", raw, "")
	if preview.Code != http.StatusOK {
		t.Fatal(preview.Code, preview.Body)
	}
	result := decodeAdminData(t, preview)
	if result["ready"] != true {
		t.Fatal(preview.Body)
	}
	input.Baseline = result["baseline"].(string)
	raw, _ = json.Marshal(input)
	var receipt map[string]any
	for range 2 {
		applied := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/migration/apply", raw, "")
		if applied.Code != http.StatusOK || s.protocolRuntimeError() != nil {
			t.Fatal(applied.Code, applied.Body, s.protocolRuntimeError())
		}
		current := decodeAdminData(t, applied)
		if receipt != nil && current["planHash"] != receipt["planHash"] {
			t.Fatal("repeated API apply created a different migration")
		}
		receipt = current
	}
	s.protocolRuntimeReady.Store(false)
	reload = revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/reload", nil, "")
	if reload.Code != http.StatusOK || s.protocolRuntimeError() != nil {
		t.Fatal("reload did not restore readiness", reload.Code, reload.Body)
	}
	input.Definitions = nil
	raw, _ = json.Marshal(input)
	changed := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/migration/apply", raw, "")
	if changed.Code != http.StatusConflict {
		t.Fatal("different intent reused old receipt", changed.Code, changed.Body)
	}
}

func TestProtocolUpgradeSourceLessModelAndExplicitCapabilityConflicts(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	model := storage.Model{ID: "standalone", Name: "standalone", Platform: "openai", Enabled: true, Origin: "manual"}
	if err := s.store.ReplaceSourceModels(t.Context(), storage.ModelSource{}, []storage.Model{model}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil || !preview.Ready || len(preview.Bindings) != 1 {
		t.Fatal(preview, err)
	}
	if _, err := s.store.ApplyProtocolUpgrade(t.Context(), preview.plan); err != nil {
		t.Fatal("source-less model cannot persist", err)
	}
	binding := preview.Bindings[0]
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = true
	preview, err = s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{Bindings: []storage.ProtocolBinding{binding}})
	if err != nil || preview.Ready {
		t.Fatal("override exceeded stored model capability", preview, err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "text-only", Name: "text-only"}); err != nil {
		t.Fatal(err)
	}
	binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID = "group", "", "", "text-only"
	preview, err = s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{Bindings: []storage.ProtocolBinding{binding}})
	if err != nil || preview.Ready {
		t.Fatal("override exceeded stored group capability", preview, err)
	}
}
