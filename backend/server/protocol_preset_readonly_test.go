package server

import (
	"strings"
	"testing"
)

// 预置协议只读：所有作者路径（编辑器、Agent CLI、REST）共用同一服务层，
// SaveDraft/Activate 对四个预置 ID 一律拒绝，定制从副本开始。
func TestPresetProtocolsAreReadOnly(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"chat-completions-api", "responses-api", "anthropic-api", "gemini-api"} {
		raw := []byte(`{"id":"` + id + `"}`)
		if _, _, err := service.SaveDraft(t.Context(), id, raw, ""); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("SaveDraft accepted preset %s: %v", id, err)
		}
		if _, err := service.Activate(t.Context(), id, "0000000000000000000000000000000000000000000000000000000000000000", ""); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("Activate accepted preset %s: %v", id, err)
		}
	}
}
