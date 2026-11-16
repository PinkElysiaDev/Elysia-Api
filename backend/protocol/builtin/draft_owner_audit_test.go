package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func newDraftAuditOptions() p.EvaluationContext {
	return fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}})
}

func encodeDraftRequest(t *testing.T, compiled *p.Compiled, request *p.Request) p.Value {
	t.Helper()
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateRequestOutput(t.Context(), wire, newDraftAuditOptions()); err != nil {
		t.Fatal(err)
	}
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("encoding changed the caller's request", err)
	}
	return wire
}

func requireDraftRejection(t *testing.T, err error, expectedDetail string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Code == "" || failure.Issues[0].Path == "" {
		t.Fatal("missing structured rejection", err)
	}
	if expectedDetail != "" && !strings.Contains(err.Error(), expectedDetail) {
		t.Fatal("rejection lost its expected detail", expectedDetail, err)
	}
}

func TestDraftRequestCacheRemainders(t *testing.T) {
	for _, fixture := range []struct{ codec, name, wire string }{
		{Chat, "known-and-unknown", `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":{"mode":"implicit","ttl":"1h","future":{"n":9007199254740993,"empty":null}}}`},
		{Chat, "unknown-only", `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":{"future":null}}`},
		{Chat, "empty", `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":{}}`},
		{Chat, "null", `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":null}`},
		{Responses, "root-and-cache", `{"model":"m","input":"hi","future":{"count":9007199254740993,"empty":null},"prompt_cache_options":{"mode":"explicit","ttl":"30m","future":{"items":[null,1e2,"value"]}}}`},
		{Responses, "cache-only", `{"model":"m","input":"hi","prompt_cache_options":{"future":null}}`},
		{Responses, "empty-cache", `{"model":"m","input":"hi","prompt_cache_options":{}}`},
		{Responses, "null-cache", `{"model":"m","input":"hi","prompt_cache_options":null}`},
		{Gemini, "mixed", `{"contents":[],"prompt_cache_options":{"future":{"items":[null,1e2,{}]}},"future_root":{"n":9007199254740993,"empty":null},"generationConfig":{"temperature":0.25,"future_gen":{"enabled":false}}}`},
		{Gemini, "unknown-null", `{"contents":[],"prompt_cache_options":{"future":null}}`},
		{Gemini, "empty", `{"contents":[],"prompt_cache_options":{}}`},
		{Gemini, "null", `{"contents":[],"prompt_cache_options":null}`},
		{Gemini, "system-owner", `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}],"vendor":{"n":9007199254740993}},"contents":[],"prompt_cache_options":{"future":null}}`},
	} {
		t.Run(fixture.codec+"/"+fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, fixture.codec)
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			for _, hasNative := range []bool{true, false} {
				candidate := request.Clone()
				if !hasNative {
					candidate.Native = nil
				}
				wire := encodeDraftRequest(t, compiled, candidate)
				expected := fixture.wire
				if !hasNative && fixture.codec == Responses {
					expected = strings.Replace(expected, `"input":"hi"`, `"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`, 1)
				}
				if !hasNative && fixture.codec == Gemini && fixture.name == "system-owner" {
					expected = strings.Replace(expected, `"role":"user",`, "", 1)
				}
				sameJSON(t, wire.Bytes(), expected)
				if hasNative && wire.JSONString() != fixture.wire {
					t.Fatal("unchanged native request lost its spelling")
				}
			}
			if fixture.codec == Responses {
				request.Model = p.StringValue("routed")
				wire := encodeDraftRequest(t, compiled, request)
				sameJSON(t, wire.Bytes(), strings.Replace(fixture.wire, `"model":"m"`, `"model":"routed"`, 1))
			}
			if fixture.codec == Chat && fixture.name == "known-and-unknown" {
				checkDraftCacheEdits(t, compiled, request, fixture.wire)
			}
		})
	}
}

func checkDraftCacheEdits(t *testing.T, compiled *p.Compiled, request *p.Request, raw string) {
	t.Helper()
	candidate := request.Clone()
	for index := range candidate.Cache {
		if candidate.Cache[index].Kind == "mode" {
			candidate.Cache[index].Value = p.StringValue("explicit")
		}
	}
	wire := encodeDraftRequest(t, compiled, candidate)
	sameJSON(t, wire.Bytes(), strings.Replace(raw, `"implicit"`, `"explicit"`, 1))
	candidate = request.Clone()
	owner, err := candidate.Parameters["wire:openai_chat"].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	delete(owner, "prompt_cache_options")
	candidate.Parameters["wire:openai_chat"] = object(owner)
	wire = encodeDraftRequest(t, compiled, candidate)
	sameJSON(t, wire.Bytes(), `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":{"mode":"implicit","ttl":"1h"}}`)
}

func TestDraftRequestOwnerMerges(t *testing.T) {
	const chatWire = `{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false},"strict":true}}}`
	for _, fixture := range []struct{ name, owner, path string }{
		{"empty", `{}`, ""},
		{"nested-union", `{"response_format":{"json_schema":{"schema":{"description":"synthetic"}}}}`, "/parameters/outputConstraint"},
		{"equal-leaf-conflict", `{"response_format":{"json_schema":{"schema":{"type":"object"}}}}`, "/parameters/outputConstraint"},
		{"null-owner", `null`, "expected JSON object"},
	} {
		t.Run("chat/"+fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, Chat)
			request, err := compiled.DecodeRequest(t.Context(), []byte(chatWire), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			request.Parameters["wire:openai_chat"] = testValue(t, fixture.owner)
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if fixture.path != "" {
				requireDraftRejection(t, err, fixture.path)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire.Bytes(), chatWire)
		})
	}
	for _, fixture := range []struct{ name, owner, conflict string }{
		{"generation-union", `{"generationConfig":{"future":{"n":9007199254740993,"empty":null,"spelling":1e2}}}`, ""},
		{"nested-schema-union", `{"generationConfig":{"responseSchema":{"description":"synthetic","properties":{"answer":{"description":"current answer"}}}}}`, "/parameters/outputConstraint"},
		{"equal-leaf-conflict", `{"generationConfig":{"temperature":0.25}}`, "/generationConfig/temperature"},
		{"null-leaf-conflict", `{"generationConfig":{"temperature":null}}`, "/generationConfig/temperature"},
		{"equal-array-conflict", `{"generationConfig":{"stopSequences":[]}}`, "/generationConfig/stopSequences"},
		{"object-null-conflict", `{"generationConfig":{"responseSchema":null}}`, "/parameters/outputConstraint"},
	} {
		t.Run("gemini/"+fixture.name, func(t *testing.T) {
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{
				"temperature": testValue(t, `0.25`), "stop": testValue(t, `[]`),
				"outputConstraint": testValue(t, `{"version":1,"kind":"json-schema","dialect":"json-schema","enforcement":"required","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}`),
				"wire:gemini":      testValue(t, fixture.owner),
			}}
			compiled := testCompiled(t, Gemini)
			before, _ := p.EncodeValue(request)
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if fixture.conflict != "" {
				requireDraftRejection(t, err, fixture.conflict)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				expected := `{"contents":[],"generationConfig":{"temperature":0.25,"stopSequences":[],"responseMimeType":"application/json","responseJsonSchema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}}`
				expected = strings.Replace(expected, `"temperature":`, `"future":{"n":9007199254740993,"empty":null,"spelling":1e2},"temperature":`, 1)
				sameJSON(t, wire.Bytes(), expected)
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("owner merge mutated its caller")
			}
		})
	}
}

func TestDraftGeminiOwnerContainers(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	for _, owner := range []string{"", `{}`, `null`} {
		t.Run("owner="+owner, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(`{"contents":[]}`), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			if owner != "" {
				request.Parameters = p.Object{"wire:gemini": testValue(t, owner)}
			}
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if owner == "null" {
				requireDraftRejection(t, err, "")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire.Bytes(), `{"contents":[]}`)
		})
	}
}

func TestDraftChatNamedToolChoice(t *testing.T) {
	const raw = `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`
	compiled := testCompiled(t, Chat)
	request, err := compiled.DecodeRequest(t.Context(), []byte(raw), newDraftAuditOptions())
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	wire := encodeDraftRequest(t, compiled, request)
	sameJSON(t, wire.Bytes(), raw)
}
