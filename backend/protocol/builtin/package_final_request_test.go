package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageFinalRequestStructure(t *testing.T) {
	for _, protocol := range []struct {
		name  string
		cases []finalWireCase
	}{
		{"openai-chat", chatFinalRequestCases()},
		{"openai-responses", responsesFinalRequestCases()},
		{"anthropic", anthropicFinalRequestCases()},
		{"google-generate-content", geminiFinalRequestCases()},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			draft := compileBuiltinPackage(t, protocol.name)
			cases := append([]finalWireCase{
				{"null-root", `null`, ""},
				{"array-root", `[]`, ""},
			}, protocol.cases...)
			for _, fixture := range cases {
				t.Run(fixture.name, func(t *testing.T) {
					checkFinalRequestCase(t, draft, fixture)
				})
			}
		})
	}
}

func checkFinalRequestCase(t *testing.T, compiled *p.Compiled, fixture finalWireCase) p.ConversionIssue {
	t.Helper()
	err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, fixture.wire))
	if fixture.path == acceptedFinalWire {
		if err != nil {
			t.Fatal("valid final request rejected", err)
		}
		return p.ConversionIssue{}
	}
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != fixture.path {
		t.Fatal("final request did not reject the invalid wire location", fixture.path, err)
	}
	return failure.Issues[0]
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
		{"null-message", `{"model":"m","messages":[null]}`, "/messages/0"},
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
		fixture("escaped-id-matches", call(`"\u0063"`)+`,`+result(`"\u0063"`), acceptedFinalWire),
		fixture("equivalent-id-spelling", call(`"c"`)+`,`+result(`"\u0063"`), acceptedFinalWire),
		fixture("whitespace-call-id", call(`" "`)+`,`+result(`" "`), acceptedFinalWire),
		fixture("unfinished-calls", call(`"c"`), "/messages/0/tool_calls/0/id"),
		fixture("parallel-result-missing", parallel+`,`+result(`"first"`), "/messages/0/tool_calls/1/id"),
		fixture("interrupted-calls", call(`"c"`)+`,{"role":"user","content":"interruption"},`+result(`"c"`), "/messages/1/role"),
		fixture("unmatched-result", call(`"c"`)+`,`+result(`"other"`), "/messages/1/tool_call_id"),
		fixture("orphan-result", result(`"c"`), "/messages/0/tool_call_id"),
		fixture("duplicate-result", call(`"c"`)+`,`+result(`"c"`)+`,`+result(`"c"`), "/messages/2/tool_call_id"),
		fixture("duplicate-pending-id", `{"role":"assistant","tool_calls":[{"id":"c"},{"id":"c"}]}`, "/messages/0/tool_calls/1/id"),
		fixture("null-call-id", call(`null`), "/messages/0/tool_calls/0/id"),
		fixture("empty-call-id", call(`""`), "/messages/0/tool_calls/0/id"),
		fixture("null-tool-list", `{"role":"user","content":"hi","tool_calls":null}`, acceptedFinalWire),
		fixture("empty-tool-list", `{"role":"user","content":"hi","tool_calls":[]}`, acceptedFinalWire),
		fixture("invalid-tool-list", `{"role":"assistant","tool_calls":false}`, "/messages/0/tool_calls"),
		fixture("invalid-tool-owner", `{"role":"user","tool_calls":[{}]}`, "/messages/0/tool_calls"),
		fixture("tool-call-object", `{"role":"assistant","tool_calls":[null]}`, "/messages/0/tool_calls/0"),
	}
}

func TestChatPackageFinalRequestControls(t *testing.T) {
	draft := compileBuiltinPackage(t, "openai-chat")
	for _, fixture := range []finalWireCase{
		{"null-options", `{"model":"m","messages":[],"stream_options":null}`, acceptedFinalWire},
		{"empty-options", `{"model":"m","messages":[],"stream_options":{}}`, acceptedFinalWire},
		{"include-usage-and-unknown", `{"model":"m","messages":[],"stream_options":{"include_usage":true,"future":{"number":9007199254740993,"value":null}}}`, acceptedFinalWire},
		{"usage-disabled", `{"model":"m","messages":[],"stream_options":{"include_usage":false}}`, acceptedFinalWire},
		{"null-single-limit", `{"model":"m","messages":[],"max_tokens":null}`, acceptedFinalWire},
		{"single-completion-limit", `{"model":"m","messages":[],"max_completion_tokens":0}`, acceptedFinalWire},
		{"nonobject-options", `{"model":"m","messages":[],"stream_options":false}`, "/stream_options"},
		{"null-include-usage", `{"model":"m","messages":[],"stream_options":{"include_usage":null}}`, "/stream_options/include_usage"},
		{"nonboolean-include-usage", `{"model":"m","messages":[],"stream_options":{"include_usage":"true"}}`, "/stream_options/include_usage"},
		{"both-null-limits", `{"model":"m","messages":[],"max_tokens":null,"max_completion_tokens":null}`, "/"},
		{"options-before-limit-conflict", `{"model":"m","messages":[],"stream_options":{"include_usage":null},"max_tokens":1,"max_completion_tokens":1}`, "/stream_options/include_usage"},
		{"valid-options-limit-conflict", `{"model":"m","messages":[],"stream_options":{"include_usage":true},"max_tokens":1,"max_completion_tokens":1}`, "/"},
		{"role-before-options", `{"model":"m","messages":[{"role":"future"}],"stream_options":false}`, "/messages/0/role"},
		{"options-before-content-decode", `{"model":"m","messages":[{"role":"user","content":false}],"stream_options":false}`, "/stream_options"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			checkFinalRequestCase(t, draft, fixture)
		})
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
		{"mixed-function-part", `{"contents":[{"role":"model","parts":[{"text":"visible","functionCall":{"id":"c","name":"lookup","args":{}}}]}]}`, "/contents/0/parts/0"},
	}
}

func TestPackageFinalRequestToolAssociations(t *testing.T) {
	for _, protocol := range []struct {
		name  string
		cases []finalWireCase
	}{
		{"openai-chat", []finalWireCase{
			{"duplicate-id-in-later-turn", `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"done"},{"role":"assistant","tool_calls":[{"id":"\u0063","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"\u0063","content":"done"}]}`, "/messages/2/tool_calls/0/id"},
		}},
		{"openai-responses", []finalWireCase{
			{"orphan-output", `{"model":"m","input":[{"type":"function_call_output","call_id":"missing","output":"done"}]}`, "/input/0/call_id"},
			{"function-and-custom-rounds", `{"model":"m","input":[{"type":"function_call","id":"item_1","call_id":"c","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"\u0063","output":"done"},{"type":"custom_tool_call","id":"item_2","call_id":"d","name":"patch","input":"patch text"},{"type":"custom_tool_call_output","call_id":"d","output":"applied"}]}`, acceptedFinalWire},
		}},
		{"anthropic", []finalWireCase{
			{"orphan-result", `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"done"}]}]}`, "/messages/0/content/0/tool_use_id"},
			{"tool-rounds", `{"model":"m","max_tokens":32,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"\u0063","content":"done"}]},{"role":"assistant","content":[{"type":"tool_use","id":"d","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"d","content":"done"}]}]}`, acceptedFinalWire},
		}},
		{"google-generate-content", []finalWireCase{
			{"orphan-result", `{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, "/contents/0/parts/0/functionResponse/name"},
			{"explicit-result-leaves-unique-pending-call", `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup","args":{}}},{"functionCall":{"id":"b","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"a","name":"lookup","response":{}}},{"functionResponse":{"name":"lookup","response":{}}}]}]}`, acceptedFinalWire},
			{"anonymous-rounds", `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, acceptedFinalWire},
			{"ambiguous-result", `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}},{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, "/contents/1/parts/0/functionResponse/name"},
			{"null-id-is-not-implicit", `{"contents":[{"role":"model","parts":[{"functionCall":{"id":null,"name":"lookup","args":{}}}]}]}`, "/contents/0/parts/0/functionCall/id"},
		}},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, protocol.name)
			for _, fixture := range protocol.cases {
				t.Run(fixture.name, func(t *testing.T) {
					issue := checkFinalRequestCase(t, compiled, fixture)
					if fixture.path != acceptedFinalWire && issue.Code != p.InvalidAssociation {
						t.Fatal("tool relation failure lost its classification", issue)
					}
					_, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), p.EvaluationContext{})
					if fixture.path == acceptedFinalWire {
						if err != nil {
							t.Fatal("valid source tool history rejected", err)
						}
					} else {
						var failure *p.ConversionError
						if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.InvalidAssociation || failure.Issues[0].Path != fixture.path {
							t.Fatal("source association lost its actual wire location", fixture.path, err)
						}
					}
				})
			}
		})
	}
}
