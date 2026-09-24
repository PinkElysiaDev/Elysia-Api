package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
)

// runtime-config 的 agentRemote 块：GET 默认值、PUT 合并语义（缺省子字段
// 保持原值、显式空串清空 publicUrl）、publicUrl 校验、落盘与热生效。

func TestRuntimeConfigAgentRemote(t *testing.T) {
	s := newOpsTestServer(t)
	// 落盘需要真实 config.json：给集成服务器换上带路径的配置。
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	s.config = cfg

	put := func(body string) int {
		c, rec := adminProtocolContext(http.MethodPut, "/api/admin/runtime-config", body)
		s.adminUpdateRuntimeConfig(c)
		return rec.Code
	}
	snapshot := func() (bool, string) {
		c, rec := adminProtocolContext(http.MethodGet, "/api/admin/runtime-config", "")
		s.adminRuntimeConfig(c)
		var envelope struct {
			Data struct {
				AgentRemote struct {
					Enabled   bool   `json:"enabled"`
					PublicURL string `json:"publicUrl"`
				} `json:"agentRemote"`
			} `json:"data"`
		}
		if err := decodeJSONBody(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode GET: %v", err)
		}
		return envelope.Data.AgentRemote.Enabled, envelope.Data.AgentRemote.PublicURL
	}

	// 默认：启用、无对外地址。
	if enabled, publicURL := snapshot(); !enabled || publicURL != "" {
		t.Fatalf("default = (%v, %q)", enabled, publicURL)
	}

	// 只关开关：publicUrl 不受影响（合并语义）。
	if code := put(`{"agentRemote":{"enabled":false}}`); code != http.StatusOK {
		t.Fatalf("disable: %d", code)
	}
	if enabled, publicURL := snapshot(); enabled || publicURL != "" {
		t.Fatalf("after disable = (%v, %q)", enabled, publicURL)
	}

	// 只设地址：enabled 保持 false。
	if code := put(`{"agentRemote":{"publicUrl":"https://gw.example.com"}}`); code != http.StatusOK {
		t.Fatalf("set url: %d", code)
	}
	if enabled, publicURL := snapshot(); enabled || publicURL != "https://gw.example.com" {
		t.Fatalf("after set url = (%v, %q)", enabled, publicURL)
	}

	// 显式空串清空地址；enabled 不动。
	if code := put(`{"agentRemote":{"publicUrl":""}}`); code != http.StatusOK {
		t.Fatalf("clear url: %d", code)
	}
	if enabled, publicURL := snapshot(); enabled || publicURL != "" {
		t.Fatalf("after clear = (%v, %q)", enabled, publicURL)
	}

	// 非法地址：拒绝且不落任何改动。
	if code := put(`{"agentRemote":{"publicUrl":"not-a-url"}}`); code != http.StatusBadRequest {
		t.Fatalf("invalid url = %d", code)
	}
	if enabled, _ := snapshot(); enabled {
		t.Fatalf("invalid url must not change state")
	}

	// 热生效（内存口径）+ 落盘（重启后保留）。
	if code := put(`{"agentRemote":{"enabled":false,"publicUrl":"https://gw.example.com"}}`); code != http.StatusOK {
		t.Fatalf("apply both: %d", code)
	}
	if cfg.GetAgentRemote().AgentRemoteEnabled() {
		t.Fatalf("gate must be off in memory")
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), `"agentRemote"`) || !strings.Contains(string(raw), "gw.example.com") {
		t.Fatalf("agentRemote not persisted: %s", raw)
	}

	// 重载后保留（模拟重启读回）。
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.GetAgentRemote().AgentRemoteEnabled() || reloaded.GetAgentRemote().PublicURL != "https://gw.example.com" {
		t.Fatalf("agentRemote lost after reload: %+v", reloaded.GetAgentRemote())
	}
}

// decodeJSONBody 是测试侧小工具（信封解析）。
func decodeJSONBody(body []byte, target any) error {
	return json.Unmarshal(body, target)
}
