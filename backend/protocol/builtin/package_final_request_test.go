package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageFinalRequestStructure(t *testing.T) {
	for _, protocol := range []struct {
		codec, name string
		cases       []finalWireCase
	}{
		{Chat, "openai-chat", chatFinalRequestCases()},
		{Responses, "openai-responses", responsesFinalRequestCases()},
		{Anthropic, "anthropic", anthropicFinalRequestCases()},
		{Gemini, "google-generate-content", geminiFinalRequestCases()},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			legacy := shippedProjectionProtocol(t, protocol.codec)
			draft := compileBuiltinPackage(t, protocol.name)
			cases := append([]finalWireCase{
				{"null-root", `null`, ""},
				{"array-root", `[]`, ""},
			}, protocol.cases...)
			for _, fixture := range cases {
				t.Run(fixture.name, func(t *testing.T) {
					checkFinalWireCase(t, legacy, draft, p.EncodeRequest, fixture)
				})
			}
		})
	}
}

func chatFinalRequestCases() []finalWireCase {
	return append([]finalWireCase{
		{"empty-history", `{"model":"m","messages":[]}`, acceptedFinalWire},
		{"whitespace-model", `{"model":" ","messages":[]}`, acceptedFinalWire},
		{"escaped-role", `{"model":"m","messages":[{"role":"\u0075ser","content":"hello"}]}`, acceptedFinalWire},
		{"function-role", `{"model":"m","messages":[{"role":"function","content":"hello"}]}`, acceptedFinalWire},
		{"model-before-history", `{"model":null,"messages":null}`, "/model"},
		{"empty-model", `{"model":"","messages":[]}`, "/model"},
		{"missing-history", `{"model":"m"}`, "/messages"},
		{"null-history", `{"model":"m","messages":null}`, "/messages"},
		{"null-message", `{"model":"m","messages":[null]}`, "/"},
		{"object-prepass-before-role", `{"model":"m","messages":[{"role":"future"},null]}`, "/"},
		{"invalid-role", `{"model":"m","messages":[{"role":"future","content":"hi"}]}`, "/messages/0/role"},
		{"role-before-thinking", `{"model":"m","messages":[{"role":null,"reasoning_content":"think"}]}`, "/messages/0/role"},
		{"thinking-without-content", `{"model":"m","messages":[{"role":"assistant","reasoning_content":"think","content":null,"tool_calls":null}]}`, "/messages/0/content"},
		{"empty-thinking-without-content", `{"model":"m","messages":[{"role":"assistant","reasoning_content":""}]}`, "/messages/0/content"},
		{"thinking-with-empty-content", `{"model":"m","messages":[{"role":"assistant","reasoning_content":"think","content":""}]}`, acceptedFinalWire},
		{"thinking-with-refusal", `{"model":"m","messages":[{"role":"assistant","reasoning_content":"think","content":null,"refusal":""}]}`, acceptedFinalWire},
		{"thinking-with-tool-history", `{"model":"m","messages":[{"role":"assistant","reasoning_content":"think","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"done"}]}`, acceptedFinalWire},
	}, chatToolSequenceCases()...)
}

func chatToolSequenceCases() []finalWireCase {
	call := func(id string) string {
		return `{"role":"assistant","tool_calls":[{"id":` + id + `,"type":"function","function":{"name":"lookup","arguments":"{}"}}]}`
	}
	result := func(id string) string {
		return `{"role":"tool","tool_call_id":` + id + `,"content":"done"}`
	}
	fixture := func(name, messages, path string) finalWireCase {
		return finalWireCase{name, `{"model":"m","messages":[` + messages + `]}`, path}
	}
	const parallel = `{"role":"assistant","tool_calls":[{"id":"first","type":"function","function":{"name":"lookup","arguments":"{}"}},{"id":"second","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`
	return []finalWireCase{
		fixture("completed-tools", call(`"c"`)+`,`+result(`"c"`), acceptedFinalWire),
		fixture("parallel-results-reordered", parallel+`,`+result(`"second"`)+`,`+result(`"first"`), acceptedFinalWire),
		fixture("raw-escaped-id-matches", call(`"\u0063"`)+`,`+result(`"\u0063"`), acceptedFinalWire),
		fixture("raw-escaped-id-differs", call(`"c"`)+`,`+result(`"\u0063"`), "/messages/1/tool_call_id"),
		fixture("whitespace-call-id", call(`" "`)+`,`+result(`" "`), acceptedFinalWire),
		fixture("unfinished-calls", call(`"c"`), "/messages/0/tool_calls"),
		fixture("parallel-result-missing", parallel+`,`+result(`"first"`), "/messages/0/tool_calls"),
		fixture("interrupted-calls", call(`"c"`)+`,{"role":"user","content":"interruption"},`+result(`"c"`), "/messages/1/role"),
		fixture("unmatched-result", call(`"c"`)+`,`+result(`"other"`), "/messages/1/tool_call_id"),
		fixture("orphan-result", result(`"c"`), "/messages/0/tool_call_id"),
		fixture("duplicate-result", call(`"c"`)+`,`+result(`"c"`)+`,`+result(`"c"`), "/messages/2/tool_call_id"),
		fixture("duplicate-pending-id", `{"role":"assistant","tool_calls":[{"id":"c"},{"id":"c"}]}`, "/messages/0/tool_calls/1/id"),
		fixture("null-call-id", call(`null`), "/messages/0/tool_calls/0/id"),
		fixture("empty-call-id", call(`""`), "/messages/0/tool_calls/0/id"),
		fixture("null-tool-list", `{"role":"user","content":"hi","tool_calls":null}`, acceptedFinalWire),
		fixture("empty-tool-list", `{"role":"user","content":"hi","tool_calls":[]}`, acceptedFinalWire),
		fixture("tool-list-before-role", `{"role":"future","tool_calls":false}`, "/"),
		fixture("tool-owner-before-role", `{"role":"future","tool_calls":[{}]}`, "/messages/0/tool_calls"),
		fixture("tool-call-object", `{"role":"assistant","tool_calls":[null]}`, "/"),
		fixture("message-object-before-pending", call(`"c"`)+`,null`, "/"),
		fixture("pending-before-tool-list", call(`"c"`)+`,{"role":"user","tool_calls":false}`, "/messages/1/role"),
		fixture("result-before-tool-list", call(`"c"`)+`,{"role":"tool","tool_call_id":"other","tool_calls":false}`, "/messages/1/tool_call_id"),
	}
}

func responsesFinalRequestCases() []finalWireCase {
	return []finalWireCase{
		{"text-input", `{"model":"m","input":"hello"}`, acceptedFinalWire},
		{"empty-text", `{"model":"m","input":""}`, acceptedFinalWire},
		{"previous-response", `{"model":"m","previous_response_id":"previous"}`, acceptedFinalWire},
		{"empty-history", `{"model":"m","input":[]}`, acceptedFinalWire},
		{"model-before-input", `{"model":null,"input":null}`, "/model"},
		{"missing-input", `{"model":"m"}`, "/input"},
		{"null-input-with-previous", `{"model":"m","input":null,"previous_response_id":"previous"}`, "/input"},
		{"numeric-input", `{"model":"m","input":42}`, "/input"},
		{"null-item", `{"model":"m","input":[null]}`, "/input/0"},
		{"escaped-message-role", `{"model":"m","input":[{"type":"\u006dessage","role":"invalid","content":"hi"}]}`, "/input/0/role"},
		{"role-before-content", `{"model":"m","input":[{"role":"invalid","content":[null]}]}`, "/input/0/role"},
		{"assistant-input-text", `{"model":"m","input":[{"role":"assistant","content":[{"type":"input_text","text":"hi"}]}]}`, "/input/0/content/0/type"},
		{"assistant-part-object", `{"model":"m","input":[{"role":"assistant","content":[null]}]}`, "/input/0/content/0"},
		{"assistant-output-text", `{"model":"m","input":[{"role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`, acceptedFinalWire},
		{"assistant-null-content", `{"model":"m","input":[{"role":"assistant","content":null}]}`, acceptedFinalWire},
		{"reasoning-null-history", `{"model":"m","input":[{"type":"reasoning","id":"r","summary":[],"content":null,"encrypted_content":"synthetic"}]}`, acceptedFinalWire},
	}
}

func anthropicFinalRequestCases() []finalWireCase {
	return []finalWireCase{
		{"empty-history", `{"model":"m","max_tokens":32,"messages":[]}`, acceptedFinalWire},
		{"native-system-role", `{"model":"m","max_tokens":32,"messages":[{"role":"system","content":"instruction"}]}`, acceptedFinalWire},
		{"escaped-user-role", `{"model":"m","max_tokens":32,"messages":[{"role":"\u0075ser","content":"hello"}]}`, acceptedFinalWire},
		{"model-before-history", `{"model":false,"max_tokens":32,"messages":null}`, "/model"},
		{"missing-history", `{"model":"m","max_tokens":32}`, "/messages"},
		{"null-history", `{"model":"m","max_tokens":32,"messages":null}`, "/messages"},
		{"null-message", `{"model":"m","max_tokens":32,"messages":[null]}`, "/messages/0"},
		{"role-before-next-object", `{"model":"m","max_tokens":32,"messages":[{"role":"future"},null]}`, "/messages/0/role"},
		{"developer-role", `{"model":"m","max_tokens":32,"messages":[{"role":"developer","content":"instruction"}]}`, "/messages/0/role"},
		{"null-role", `{"model":"m","max_tokens":32,"messages":[{"role":null,"content":"hello"}]}`, "/messages/0/role"},
		{"thinking-controls", `{"model":"m","max_tokens":2048,"messages":[],"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"medium"}}`, acceptedFinalWire},
		{"role-before-thinking", `{"model":"m","max_tokens":32,"messages":[{"role":"future"}],"thinking":false}`, "/messages/0/role"},
		{"thinking-object", `{"model":"m","max_tokens":32,"messages":[],"thinking":false}`, "/parameters/anthropic_thinking"},
		{"thinking-before-output-config", `{"model":"m","max_tokens":32,"messages":[],"thinking":{"type":"future"},"output_config":false}`, "/thinking/type"},
		{"adaptive-null-budget", `{"model":"m","max_tokens":32,"messages":[],"thinking":{"type":"adaptive","budget_tokens":null}}`, "/thinking/budget_tokens"},
		{"thinking-display", `{"model":"m","max_tokens":32,"messages":[],"thinking":{"type":"adaptive","display":[]}}`, "/thinking/display"},
		{"output-config-object", `{"model":"m","max_tokens":32,"messages":[],"output_config":false}`, "/parameters/anthropic_output_config"},
	}
}

func TestPackageFinalRequestRejectsWireEdits(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Anthropic), compileBuiltinPackage(t, "anthropic")
	const original = `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"medium"}}`
	checkFinalWireCase(t, legacy, draft, p.EncodeRequest, finalWireCase{"original", original, acceptedFinalWire})
	for _, fixture := range []struct {
		name, path string
		edits      []p.Mutation
	}{
		{"thinking-budget", "/thinking/budget_tokens", []p.Mutation{{Op: p.SetValue, Path: "/thinking/budget_tokens", Value: testValue(t, `4`)}}},
		{"output-effort", "/output_config/effort", []p.Mutation{{Op: p.SetValue, Path: "/output_config/effort", Value: p.StringValue("none")}}},
		{"role-before-thinking", "/messages/0/role", []p.Mutation{{Op: p.SetValue, Path: "/thinking/budget_tokens", Value: testValue(t, `4`)}, {Op: p.SetValue, Path: "/messages/0/role", Value: p.StringValue("developer")}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			wire, err := p.ApplyMutations(testValue(t, original), fixture.edits, p.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			checkFinalWireCase(t, legacy, draft, p.EncodeRequest, finalWireCase{fixture.name, wire.JSONString(), fixture.path})
		})
	}
}

func TestPackageFinalRequestKeepsOperationConstraints(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Anthropic), compileBuiltinPackage(t, "anthropic")
	fixture := finalWireCase{"missing-operation-limit", `{"model":"m","messages":[]}`, acceptedFinalWire}
	checkFinalWireCase(t, legacy, draft, p.EncodeRequest, fixture)
	for _, operation := range []string{"generate", "stream"} {
		wire := testValue(t, fixture.wire)
		if oldErr, newErr := legacy.CheckOperationValueInput(t.Context(), operation, wire), draft.CheckOperationValueInput(t.Context(), operation, wire); oldErr == nil || newErr == nil {
			t.Fatal("final structure replaced the operation's required output limit", operation, oldErr, newErr)
		}
	}
}

func geminiFinalRequestCases() []finalWireCase {
	return []finalWireCase{
		{"empty-history", `{"contents":[]}`, acceptedFinalWire},
		{"implicit-user", `{"contents":[{"parts":[{"text":"hello"}]}]}`, acceptedFinalWire},
		{"escaped-model-role", `{"contents":[{"role":"\u006dodel","parts":[{"text":"answer"}]}]}`, acceptedFinalWire},
		{"missing-history", `{}`, "/contents"},
		{"null-history", `{"contents":null}`, "/contents"},
		{"null-message", `{"contents":[null]}`, "/contents/0"},
		{"role-before-next-object", `{"contents":[{"role":"future"},null]}`, "/contents/0/role"},
		{"system-role", `{"contents":[{"role":"system","parts":[]}]}`, "/contents/0/role"},
		{"null-role", `{"contents":[{"role":null,"parts":[]}]}`, "/contents/0/role"},
	}
}

func TestPackageFinalRequestRemainingConstraints(t *testing.T) {
	for _, fixture := range []struct {
		codec, name, wire, path, stage string
		code                          p.IssueCode
	}{
		{Chat, "openai-chat", `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"done"},{"role":"assistant","tool_calls":[{"id":"\u0063","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"\u0063","content":"done"}]}`, "/content/2/children/0/callId", "wire.output", p.InvalidAssociation},
		{Responses, "openai-responses", `{"model":"m","input":[{"type":"function_call_output","call_id":"missing","output":"done"}]}`, "/content/0/callId", "wire.output", p.InvalidAssociation},
		{Anthropic, "anthropic", `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"done"}]}]}`, "/content/0/children/0/callId", "wire.output", p.InvalidAssociation},
		{Gemini, "google-generate-content", `{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, "/functionResponse", "wire", p.UnsupportedCapability},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			legacy, draft := shippedProjectionProtocol(t, fixture.codec), compileBuiltinPackage(t, fixture.name)
			wire := testValue(t, fixture.wire)
			oldErr := legacy.ValidateWireOutput(p.EncodeRequest, wire)
			var failure *p.ConversionError
			if !errors.As(oldErr, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != fixture.code || failure.Issues[0].Stage != fixture.stage || failure.Issues[0].Path != fixture.path {
				t.Fatal("legacy remaining-constraint fixture changed", oldErr)
			}
			if err := draft.ValidateWireOutput(p.EncodeRequest, wire); err != nil {
				t.Fatal("recorded request-final gap changed; review and close this ledger entry", err)
			}
			t.Log("NOT CLOSED: request-final structure and Chat adjacency are declared; full-tree association and decoder validation remain incomplete", oldErr)
		})
	}
}
