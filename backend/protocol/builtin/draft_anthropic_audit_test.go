package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDraftAnthropicOwnerEdits(t *testing.T) {
	const original = `{"model":"m","max_tokens":8192,"messages":[],"thinking":{"type":"enabled","budget_tokens":1024,"future":{"left":9007199254740993}},"output_config":{"effort":"high","future":{"left":null}}}`
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []struct{ name, owner, expected string }{
		{"empty", `{}`, `{"model":"m","max_tokens":8192,"messages":[],"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"high"}}`},
		{"nested-union", `{"thinking":{"future":{"right":null}},"output_config":{"future":{"right":[1,2]}}}`, `{"model":"m","max_tokens":8192,"messages":[],"thinking":{"type":"enabled","budget_tokens":1024,"future":{"right":null}},"output_config":{"effort":"high","future":{"right":[1,2]}}}`},
		{"equal-leaf-single-owner", `{"thinking":{"future":{"left":9007199254740993}}}`, `{"model":"m","max_tokens":8192,"messages":[],"thinking":{"type":"enabled","budget_tokens":1024,"future":{"left":9007199254740993}},"output_config":{"effort":"high"}}`},
		{"canonical-budget-conflict", `{"thinking":{"budget_tokens":1024}}`, ""},
		{"canonical-effort-conflict", `{"output_config":{"effort":"high"}}`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(original), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			sameJSON(t, encodeDraftRequest(t, compiled, request).Bytes(), original)
			// Remainders now have one owner. Replacing that owner edits the
			// remainder; only duplicated canonical fields are merge conflicts.
			request.Parameters["wire:claude"] = testValue(t, fixture.owner)
			if fixture.expected == "" {
				_, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
				requireDraftRejection(t, err, "")
				return
			}
			wire := encodeDraftRequest(t, compiled, request)
			sameJSON(t, wire.Bytes(), fixture.expected)
		})
	}
}

func TestDraftAnthropicNativeEdits(t *testing.T) {
	const hosted = `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"web_search_20250305","name":"original","max_uses":2,"vendor":{"n":9007199254740993},"cache_control":{"type":"ephemeral","ttl":"1h","future":true}}]}`
	for _, fixture := range []struct {
		name, wire, expected string
		edit                 func(*p.Request)
	}{
		{"hosted-current-name-remove-cache", hosted, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"web_search_20250305","name":"renamed","max_uses":2,"vendor":{"n":9007199254740993}}]}`, func(request *p.Request) {
			request.Tools[0].Name = p.StringValue("renamed")
			request.Tools[0].Cache = nil
		}},
		{"opaque-remove-name-null-cache", hosted, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"web_search_20250305","max_uses":2,"vendor":{"n":9007199254740993},"cache_control":null}]}`, func(request *p.Request) {
			request.Tools[0].Kind = p.OpaqueTool
			request.Tools[0].Name = p.Value{}
			request.Tools[0].Cache = []p.CacheIntent{{Kind: "breakpoint", Location: "tool", Value: testValue(t, `null`)}}
		}},
		{"inline-system-current-position-and-content", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"inline instruction","output_config":{"effort":"high"}}]}`, `{"model":"m","max_tokens":10,"messages":[{"role":"system","content":[{"type":"text","text":"edited instruction"}],"output_config":{"effort":"high"}},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`, func(request *p.Request) {
			request.Content[1].Children[0].Payload = p.StringValue("edited instruction")
			request.Content[0], request.Content[1] = request.Content[1], request.Content[0]
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, Anthropic)
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			fixture.edit(request)
			wire := encodeDraftRequest(t, compiled, request)
			sameJSON(t, wire.Bytes(), fixture.expected)
		})
	}
}

func TestDraftAnthropicNativeFreeCapacity(t *testing.T) {
	for _, fixture := range []struct {
		name          string
		bytes, blocks int
	}{{"large-payload", 3 << 20, 0}, {"many-nodes", 4, 1800}} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, Anthropic)
			raw := largeAnthropicRequest(fixture.bytes, fixture.blocks)
			request, err := compiled.DecodeRequest(t.Context(), raw, newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			wire := encodeDraftRequest(t, compiled, request)
			sameJSON(t, wire.Bytes(), string(raw))
		})
	}
}
