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

// The provider processes breakpoints tools then system then messages and
// documents that a longer TTL should appear before a shorter one. No reference
// implementation enforces that order and the provider does not document the
// failing status, so the encoder records the caller's layout as a non-blocking
// warning rather than rejecting a request that would otherwise cache correctly —
// a 400 here would suppress exactly the caching this path exists to preserve.
func TestCacheTTLOrderingIsReportedNotRejected(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}, Diagnostics: &p.DiagnosticSink{}}
	request := &p.Request{
		SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"),
		Tools: []p.Tool{{Kind: p.FunctionTool, Name: p.StringValue("f"), InputSchema: testValue(t, `{"type":"object"}`),
			Cache: []p.CacheIntent{{Kind: "breakpoint", Location: "tool", Value: testValue(t, `{"type":"ephemeral"}`), TTL: p.StringValue("1h")}}}},
		Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi"),
			Cache: []p.CacheIntent{{Kind: "breakpoint", Location: "block", Value: testValue(t, `{"type":"ephemeral"}`), TTL: p.StringValue("5m")}}}}}},
	}
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatalf("1h before 5m must be accepted: %v", err)
	}
	if len(options.Diagnostics.Issues()) != 0 {
		t.Fatalf("valid ordering produced a warning: %v", options.Diagnostics.Issues())
	}
	// The same markers reversed: a 5m tool marker now precedes a 1h message one.
	request.Tools[0].Cache[0].TTL = p.StringValue("5m")
	request.Content[0].Children[0].Cache[0].TTL = p.StringValue("1h")
	options.Diagnostics = &p.DiagnosticSink{}
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatalf("out-of-order TTL must still encode: %v", err)
	}
	if len(options.Diagnostics.Issues()) == 0 {
		t.Fatal("a 1h breakpoint after a 5m one must be reported as a warning")
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
