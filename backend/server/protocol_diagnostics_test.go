package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestAnthropicCopySystemCacheGateway(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, synthesis := range []bool{false, true} {
			for _, system := range []string{`"private system"`, `[{"type":"text","text":"private system"},{"type":"text","text":"prefix","cache_control":{"type":"ephemeral","ttl":"1h","extension":{"priority":9007199254740993}}}]`} {
				t.Run(fmt.Sprintf("stream=%v/synthesis=%v/blocks=%v", stream, synthesis, strings.HasPrefix(system, "[")), func(t *testing.T) {
					var captured map[string]any
					provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						decoder := json.NewDecoder(r.Body)
						decoder.UseNumber()
						if err := decoder.Decode(&captured); err != nil {
							t.Error(err)
						}
						fixture := cacheWireFixtures()[1]
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							io.WriteString(w, fixture.stream)
						} else {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, fixture.response)
						}
					}))
					defer provider.Close()
					definition := presetDefinition(t, "anthropic-messages")
					definition.ID = "anthropic-messages-copy"
					groups := presetGroup(t, "custom:anthropic-messages-copy", provider.URL)
					s := newTestServerWithStore(t, groups, definition)
					sources, err := s.store.ListSources(t.Context())
					if err != nil || len(sources) != 1 {
						t.Fatalf("expected imported source: %v, %v", sources, err)
					}
					sources[0].CacheSynthesis = synthesis
					if err := s.store.UpsertSource(t.Context(), sources[0]); err != nil {
						t.Fatal(err)
					}
					s.invalidateRouteCache()
					body := fmt.Sprintf(`{"model":"grp","max_tokens":64,"stream":%v,"system":%s,"messages":[{"role":"user","content":"hello"}]}`, stream, system)
					c, rec := messagesRequestContext(body)
					s.chatCompletions(c)
					if rec.Code != 200 {
						t.Fatal(rec.Code, rec.Body)
					}
					wire, _ := json.Marshal(captured)
					if !strings.Contains(string(wire), "private system") {
						t.Fatal("system dropped", string(wire))
					}
					if strings.HasPrefix(system, "[") && !strings.Contains(string(wire), `"ttl":"1h"`) {
						t.Fatal("caller cache marker lost", string(wire))
					}
					if strings.HasPrefix(system, "[") && !strings.Contains(string(wire), `"priority":9007199254740993`) {
						t.Fatal("cache extension changed", string(wire))
					}
					if synthesis && !strings.Contains(string(wire), "cache_control") {
						t.Fatal("synthesis did not reach upstream")
					}
					logs := latestUsageRecords(t, s)
					raw, _, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
					if err != nil {
						t.Fatal(err)
					}
					var record usageRecord
					if err := json.Unmarshal(raw, &record); err != nil {
						t.Fatal(err)
					}
					if record.TargetFormat != "anthropic-messages-copy" || record.UpstreamRevision == "" || record.CacheSynthesis != synthesis {
						t.Fatal("candidate diagnostics missing")
					}
					diagnostic, _ := json.Marshal(record.SystemStructure)
					if strings.Contains(string(diagnostic), "private system") || strings.Contains(string(diagnostic), "prefix") {
						t.Fatal("prompt leaked into structural diagnostic")
					}
				})
			}
		}
	}
}

func TestEncodingFailureKeepsCandidateAndExactField(t *testing.T) {
	s := newAgentIntegrationServer(t)
	compiled, _ := s.protocolServiceInst.Pin("google-generate-content")
	request := &protocol.Request{SchemaVersion: protocol.SemanticSchemaVersion, Source: protocol.AgentIdentity(), Model: protocol.StringValue("m"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("system"), ID: protocol.StringValue("sensitive-id"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("secret prompt")}}}}, Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, "64")}}
	candidate := gatewayCandidate{compiled: compiled, binding: protocol.Binding{ProtocolID: "google-generate-content"}, operation: compiled.Operations()["generate"]}
	candidate.model.Name = "candidate"
	candidate.model.BaseURL = "https://example.invalid"
	candidate.model.SourceID = "source"
	c, _ := messagesRequestContext(`{}`)
	record := &usageRecord{}
	err := s.forwardGateway(c, record, &gatewayPlan{request: request}, candidate)
	// Anthropic 目标现按位转换带身份的 system；Gemini 目标仍显式拒绝（无网络依赖）。
	if err == nil || !strings.Contains(err.Error(), "/content/0") {
		t.Fatal(err)
	}
	if record.ModelName != "candidate" || record.SourceID != "source" || record.UpstreamRevision == "" {
		t.Fatal("failure lost candidate")
	}
	data, _ := json.Marshal(record.SystemStructure)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "sensitive") {
		t.Fatal("diagnostics exposed values")
	}
}
