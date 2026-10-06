package server

import (
	"encoding/json"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestEnabledProtocolCatalogUsesPinnedRevision(t *testing.T) {
	s := newAgentIntegrationServer(t)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	compiled, _ := service.Pin("chat-completions-api")
	// 预置只读：未验证草稿只能以副本 ID 存在（原实现会在预置 ID 上存草稿）。
	definition := compiled.Definition()
	definition.Name = "unverified draft"
	definition.ID = "unactivated-copy"
	delete(definition.Operations, "models")
	definition.Agent = nil
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SaveDraft(t.Context(), definition.ID, raw, ""); err != nil {
		t.Fatal(err)
	}
	c, response := adminProtocolContext("GET", "/api/admin/protocols/enabled", "")
	s.adminEnabledProtocols(c)
	var envelope struct {
		Data struct {
			Items []enabledProtocol `json:"items"`
		} `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 4 {
		t.Fatal("draft leaked into enabled selector", response.Body.String())
	}
	for _, item := range envelope.Data.Items {
		if item.ID != "chat-completions-api" {
			continue
		}
		if item.Revision != compiled.Hash() || item.Name == "unverified draft" || !item.CanGenerate || !item.HasAgentPolicy || !item.HasModelDiscovery || !item.Capabilities[protocol.FunctionToolsCapability] {
			t.Fatal(item)
		}
	}
}
