package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func compileBuiltinModule(t *testing.T, name string) *protocol.Compiled {
	t.Helper()
	adapter, err := findWireAdapter(name)
	if err != nil {
		t.Fatal(err)
	}
	definition := protocol.Definition{SchemaVersion: 2, ID: "arbitrary-" + name, Name: name, Version: "1", Family: string(adapter.format), WireVersion: legacyWireContractVersion, Native: protocol.NativePolicy{Preserve: true}, Directions: map[protocol.Direction]protocol.Mapping{}, Capabilities: protocol.CapabilitySet{}, Operations: map[string]protocol.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}}}}
	for _, direction := range (builtinProtocolModule{adapter: adapter}).Directions() {
		definition.Directions[direction] = protocol.Mapping{Module: name}
	}
	for _, capability := range protocol.CapabilityCatalog() {
		definition.Capabilities[capability] = true
	}
	compiler, err := NewProtocolCompiler(protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(body)
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestCompiledBuiltinRequestMatrix(t *testing.T) {
	fixtures := map[string]string{
		"openai-chat": `{"model":"m","messages":[{"role":"system","content":"rules"},{"role":"user","content":"hello"},{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}]},{"role":"tool","tool_call_id":"c","content":"found"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
		"responses":   `{"model":"m","input":[{"role":"system","content":"rules"},{"role":"user","content":"hello"},{"type":"function_call","call_id":"c","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"c","output":"found"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`,
		"anthropic":   `{"model":"m","max_tokens":100,"system":"rules","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"found"}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`,
		"gemini":      `{"systemInstruction":{"parts":[{"text":"rules"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"n":9007199254740993}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"found"}}}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`,
	}
	options := protocol.EvaluationContext{Scope: protocol.Scope{Model: "m"}}
	for source, fixture := range fixtures {
		for target := range fixtures {
			t.Run(source+"/"+target, func(t *testing.T) {
				decoder, encoder := compileBuiltinModule(t, source), compileBuiltinModule(t, target)
				request, err := decoder.DecodeRequest(t.Context(), []byte(fixture), options)
				if err != nil {
					t.Fatal(err)
				}
				body, err := encoder.EncodeRequest(t.Context(), request, options)
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{"rules", "hello", "lookup", "found", "9007199254740993"} {
					if !strings.Contains(string(body), text) {
						t.Fatalf("lost %s: %s", text, body)
					}
				}
			})
		}
	}
}

func TestCompiledResponsesCustomHistoryAndEdits(t *testing.T) {
	compiled := compileBuiltinModule(t, "responses")
	input := []byte(`{"model":"m","vendor":false,"tools":[{"type":"custom","name":"shell","format":{"type":"text"}}],"input":[{"role":"user","content":"hello"},{"type":"custom_tool_call","call_id":"c","name":"shell","input":"echo hello"},{"type":"custom_tool_call_output","call_id":"c","output":"hello"},{"role":"user","content":"again"}]}`)
	request, err := compiled.DecodeRequest(t.Context(), input, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Model = protocol.StringValue("changed")
	request.Tools[0].Name = protocol.StringValue("new_shell")
	body, err := compiled.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`"model":"changed"`, `"name":"new_shell"`, "echo hello", "custom_tool_call_output", "again", `"vendor":false`} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("lost %s: %s", text, body)
		}
	}
	request.Tools = nil
	body, err = compiled.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"tools"`) {
		t.Fatalf("tools resurrected: %s", body)
	}
}

func TestCompiledBuiltinResponseMatrix(t *testing.T) {
	fixtures := map[string]string{
		"openai-chat": `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":40}}}`,
		"responses":   `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},{"type":"function_call","call_id":"c","name":"lookup","arguments":"{\"n\":9007199254740993}"}],"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":40}}}`,
		"anthropic":   `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"c","name":"lookup","input":{"n":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":60,"output_tokens":10,"cache_read_input_tokens":40}}`,
		"gemini":      `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"},{"functionCall":{"id":"c","name":"lookup","args":{"n":9007199254740993}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"cachedContentTokenCount":40}}`,
	}
	for source, fixture := range fixtures {
		for target := range fixtures {
			t.Run(source+"/"+target, func(t *testing.T) {
				decoder, encoder := compileBuiltinModule(t, source), compileBuiltinModule(t, target)
				response, err := decoder.DecodeResponse(t.Context(), []byte(fixture), protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				body, err := encoder.EncodeResponse(t.Context(), response, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				for _, expected := range []string{"hello", "lookup", "9007199254740993"} {
					if !strings.Contains(string(body), expected) {
						t.Fatalf("lost %s: %s", expected, body)
					}
				}
				decoded, err := encoder.DecodeResponse(t.Context(), body, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				if decoded.Usage == nil || decoded.Usage.CacheRead == nil || decoded.Usage.CacheRead.Count != 40 || decoded.Usage.Input == nil || decoded.Usage.Input.Count != 100 {
					t.Fatalf("lost usage: %+v; wire %s", decoded.Usage, body)
				}
			})
		}
	}
}
