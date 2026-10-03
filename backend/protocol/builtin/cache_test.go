package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestCacheTTLEditsOverrideNativeAtEveryScope(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	wire := `{"model":"m","max_tokens":64,"cache_control":{"type":"ephemeral","ttl":"1h"},"system":[{"type":"text","text":"prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YQ=="},"cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"done","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],"tools":[{"name":"f","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}]}`
	for _, ttl := range []p.Value{p.StringValue("5m"), {}, testValue(t, `null`)} {
		t.Run(string(ttl.Bytes()), func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(wire), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			edit := func(intents []p.CacheIntent) {
				for i := range intents {
					policy, err := intents[i].Value.ReadObject()
					if err != nil || !policy["ttl"].IsZero() || intents[i].TTL != p.StringValue("1h") {
						t.Fatalf("TTL did not have one semantic owner: %+v", intents[i])
					}
					intents[i].TTL = ttl
					count++
				}
			}
			var visit func([]p.Node)
			visit = func(nodes []p.Node) {
				for i := range nodes {
					edit(nodes[i].Cache)
					visit(nodes[i].Children)
				}
			}
			edit(request.Cache)
			edit(request.Tools[0].Cache)
			visit(request.Content)
			encoded, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if count != 5 || strings.Contains(string(encoded), `"1h"`) {
				t.Fatalf("native TTL revived: %s", encoded)
			}
			want := 5
			if ttl.IsZero() {
				want = 0
			}
			if strings.Count(string(encoded), `"ttl":`) != want {
				t.Fatalf("TTL scope lost: %s", encoded)
			}
		})
	}
}

func TestCacheAbsentNullAndConflictingTTL(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, control := range []string{"", `,"cache_control":null`, `,"cache_control":{"type":"ephemeral","x":9007199254740993,"ttl":null}`} {
		wire := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}]` + control + `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(wire), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, encoded, wire)
		request.Cache = []p.CacheIntent{{Kind: "breakpoint", Location: "request", Value: testValue(t, `{"type":"ephemeral","ttl":"1h"}`), TTL: p.StringValue("5m")}}
		if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
			t.Fatal("ambiguous TTL accepted")
		}
	}
}
