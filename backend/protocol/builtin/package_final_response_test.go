package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const acceptedFinalWire = "<accepted>"

type finalWireCase struct {
	name, wire, path string
}

func TestPackageFinalResponseContracts(t *testing.T) {
	for _, protocol := range []struct {
		codec, name string
		cases       []finalWireCase
	}{
		{Chat, "openai-chat", chatFinalResponseCases()},
		{Responses, "openai-responses", responsesFinalResponseCases()},
		{Anthropic, "anthropic", anthropicFinalResponseCases()},
		{Gemini, "google-generate-content", geminiFinalResponseCases()},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			legacy := shippedProjectionProtocol(t, protocol.codec)
			draft := compileBuiltinPackage(t, protocol.name)
			cases := append([]finalWireCase{
				{"null-root", `null`, ""},
				{"array-root", `[]`, ""},
				{"string-root", `"response"`, ""},
				{"error-object", `{"type":"error","error":{},"vendor":{"x":null}}`, acceptedFinalWire},
				{"error-string", `{"type":"error","error":"failure"}`, "/error"},
			}, protocol.cases...)
			for _, fixture := range cases {
				t.Run(fixture.name, func(t *testing.T) {
					checkFinalWireCase(t, legacy, draft, p.EncodeResponse, fixture)
				})
			}
		})
	}
}

func checkFinalWireCase(t *testing.T, legacy, draft *p.Compiled, direction p.Direction, fixture finalWireCase) {
	t.Helper()
	wire := testValue(t, fixture.wire)
	oldErr := legacy.ValidateWireOutput(direction, wire)
	newErr := draft.ValidateWireOutput(direction, wire)
	if fixture.path == acceptedFinalWire {
		if oldErr != nil || newErr != nil {
			t.Fatal("accepted final wire was rejected", oldErr, newErr)
		}
		return
	}
	var rejected *p.ConversionError
	if !errors.As(oldErr, &rejected) || len(rejected.Issues) != 1 || rejected.Issues[0].Path != fixture.path {
		t.Fatalf("legacy fixture expected first failure at %q: %v", fixture.path, oldErr)
	}
	checkPackageFailureRejection(t, oldErr, newErr)
}

func chatFinalResponseCases() []finalWireCase {
	const response = `{"id":"r","object":"chat.completion","model":"m","created":0,"choices":[{"index":0,"message":{"role":"assistant","content":null}}]}`
	edit := func(before, after string) string { return strings.Replace(response, before, after, 1) }
	return []finalWireCase{
		{"message", response, acceptedFinalWire},
		{"error-bypasses-success", `{"error":{},"object":false,"choices":null}`, acceptedFinalWire},
		{"unknown-message-field", edit(`"content":null`, `"content":42,"vendor":true`), acceptedFinalWire},
		{"null-error", edit(`"id":"r"`, `"error":null,"id":"r"`), acceptedFinalWire},
		{"whitespace-string", edit(`"id":"r"`, `"id":" "`), acceptedFinalWire},
		{"object-before-id", edit(`"id":"r","object":"chat.completion"`, `"id":null,"object":"other"`), "/object"},
		{"empty-id", edit(`"id":"r"`, `"id":""`), "/id"},
		{"missing-model", edit(`"model":"m",`, ``), "/model"},
		{"fractional-spelling", edit(`"created":0`, `"created":1.0`), "/created"},
		{"exponent-spelling", edit(`"created":0`, `"created":1e0`), "/created"},
		{"overflow-created", edit(`"created":0`, `"created":9223372036854775808`), "/created"},
		{"max-created", edit(`"created":0`, `"created":9223372036854775807`), acceptedFinalWire},
		{"null-choices", edit(`[{"index":0,"message":{"role":"assistant","content":null}}]`, `null`), "/choices"},
		{"null-choice", edit(`{"index":0,"message":{"role":"assistant","content":null}}`, `null`), "/choices/0"},
		{"index-before-message", edit(`{"index":0,"message":{"role":"assistant","content":null}}`, `{"index":-1,"message":null}`), "/choices/0/index"},
		{"null-message", edit(`{"role":"assistant","content":null}`, `null`), "/choices/0/message"},
		{"wrong-role", edit(`"role":"assistant"`, `"role":"user"`), "/choices/0/message/role"},
	}
}

func responsesFinalResponseCases() []finalWireCase {
	const response = `{"id":"r","object":"response","model":"m","status":"completed","created_at":0,"output":[{"id":"i","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"","annotations":[]}]}]}`
	edit := func(before, after string) string { return strings.Replace(response, before, after, 1) }
	const reasoning = `{"id":"r","object":"response","model":"m","status":"completed","created_at":0,"output":[{"id":"i","type":"reasoning","summary":[{"type":"summary_text","text":""}],"content":[{"type":"reasoning_text","text":""}]}]}`
	editReasoning := func(before, after string) string { return strings.Replace(reasoning, before, after, 1) }
	return []finalWireCase{
		{"message", response, acceptedFinalWire},
		{"reasoning", reasoning, acceptedFinalWire},
		{"error-bypasses-success", `{"error":{},"object":false,"output":null}`, acceptedFinalWire},
		{"object-before-id", edit(`"id":"r","object":"response"`, `"id":null,"object":"other"`), "/object"},
		{"model-before-status", edit(`"model":"m","status":"completed"`, `"model":null,"status":null`), "/model"},
		{"null-created", edit(`"created_at":0`, `"created_at":null`), "/created_at"},
		{"null-output", edit(`[{"id":"i","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"","annotations":[]}]}]`, `null`), "/output"},
		{"null-item", edit(`{"id":"i","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"","annotations":[]}]}`, `null`), "/output/0"},
		{"item-id-before-content", edit(`"id":"i"`, `"id":null`), "/output/0/id"},
		{"phase-before-role", edit(`"role":"assistant"`, `"phase":"other","role":"user"`), "/output/0/phase"},
		{"null-phase", edit(`"role":"assistant"`, `"phase":null,"role":"assistant"`), acceptedFinalWire},
		{"final-phase", edit(`"role":"assistant"`, `"phase":"final_answer","role":"assistant"`), acceptedFinalWire},
		{"commentary-phase", edit(`"role":"assistant"`, `"phase":"commentary","role":"assistant"`), acceptedFinalWire},
		{"wrong-role", edit(`"role":"assistant"`, `"role":"user"`), "/output/0/role"},
		{"missing-item-status", edit(`"role":"assistant","status":"completed"`, `"role":"assistant"`), "/output/0/status"},
		{"null-content", edit(`[{"type":"output_text","text":"","annotations":[]}]`, `null`), "/output/0/content"},
		{"null-part", edit(`{"type":"output_text","text":"","annotations":[]}`, `null`), "/output/0/content/0"},
		{"text-before-annotations", edit(`"text":"","annotations":[]`, `"text":null,"annotations":null`), "/output/0/content/0/text"},
		{"annotations-required", edit(`,"annotations":[]`, ``), "/output/0/content/0/annotations"},
		{"unknown-part", edit(`"type":"output_text"`, `"type":"vendor_part"`), acceptedFinalWire},
		{"unknown-item", edit(`"type":"message"`, `"type":"vendor_item"`), acceptedFinalWire},
		{"reasoning-summary-required", editReasoning(`"summary":[{"type":"summary_text","text":""}],`, ``), "/output/0/summary"},
		{"reasoning-summary-before-content", editReasoning(`"summary":[{"type":"summary_text","text":""}],"content":[{"type":"reasoning_text","text":""}]`, `"summary":null,"content":null`), "/output/0/summary"},
		{"reasoning-content-null", editReasoning(`"content":[{"type":"reasoning_text","text":""}]`, `"content":null`), "/output/0/content"},
		{"reasoning-content-optional", editReasoning(`,"content":[{"type":"reasoning_text","text":""}]`, ``), acceptedFinalWire},
		{"reasoning-part-object", editReasoning(`{"type":"summary_text","text":""}`, `null`), "/output/0/summary/0"},
		{"reasoning-type-before-text", editReasoning(`"type":"summary_text","text":""`, `"type":"reasoning_text","text":null`), "/output/0/summary/0/type"},
		{"reasoning-text-required", editReasoning(`"type":"reasoning_text","text":""`, `"type":"reasoning_text","text":null`), "/output/0/content/0/text"},
	}
}

func anthropicFinalResponseCases() []finalWireCase {
	const response = `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"","signature":""}],"usage":{"input_tokens":0,"output_tokens":0}}`
	edit := func(before, after string) string { return strings.Replace(response, before, after, 1) }
	return []finalWireCase{
		{"message", response, acceptedFinalWire},
		{"error-still-checks-message", `{"error":{}}`, "/id"},
		{"error-type-requires-object", `{"type":"error","error":null}`, "/error"},
		{"id-before-type", edit(`"id":"r"`, `"id":null`), "/id"},
		{"type-string-before-role", edit(`"type":"message","role":"assistant"`, `"type":null,"role":null`), "/type"},
		{"wrong-message-type", edit(`"type":"message"`, `"type":"other"`), ""},
		{"wrong-role", edit(`"role":"assistant"`, `"role":"user"`), ""},
		{"missing-content", edit(`"content":[{"type":"thinking","thinking":"","signature":""}],`, ``), "/content"},
		{"null-content", edit(`[{"type":"thinking","thinking":"","signature":""}]`, `null`), "/content"},
		{"null-block", edit(`{"type":"thinking","thinking":"","signature":""}`, `null`), "/content/0"},
		{"thinking-before-signature", edit(`"thinking":"","signature":""`, `"thinking":null,"signature":null`), "/content/0/thinking"},
		{"missing-signature", edit(`,"signature":""`, ``), "/content/0/signature"},
		{"unknown-block", edit(`"type":"thinking"`, `"type":"vendor_block"`), acceptedFinalWire},
		{"null-usage", edit(`{"input_tokens":0,"output_tokens":0}`, `null`), "/usage"},
		{"output-before-input", edit(`"input_tokens":0,"output_tokens":0`, `"input_tokens":null,"output_tokens":null`), "/usage/output_tokens"},
		{"missing-input", edit(`"input_tokens":0,`, ``), "/usage/input_tokens"},
		{"fractional-input", edit(`"input_tokens":0`, `"input_tokens":1.0`), "/usage/input_tokens"},
		{"cache-read-before-create", edit(`"input_tokens":0`, `"cache_read_input_tokens":null,"cache_creation_input_tokens":null,"input_tokens":0`), "/usage/cache_read_input_tokens"},
		{"cache-create-negative", edit(`"input_tokens":0`, `"cache_creation_input_tokens":-1,"input_tokens":0`), "/usage/cache_creation_input_tokens"},
	}
}

func geminiFinalResponseCases() []finalWireCase {
	const response = `{"candidates":[{"content":{"parts":[{"text":""}]}}]}`
	edit := func(before, after string) string { return strings.Replace(response, before, after, 1) }
	cases := []finalWireCase{
		{"message", response, acceptedFinalWire},
		{"candidates-optional", `{}`, acceptedFinalWire},
		{"error-bypasses-candidates", `{"error":{},"candidates":null}`, acceptedFinalWire},
		{"null-candidates", `{"candidates":null}`, "/candidates"},
		{"null-candidate", `{"candidates":[null]}`, "/candidates/0"},
		{"content-optional", `{"candidates":[{}]}`, acceptedFinalWire},
		{"null-content", `{"candidates":[{"content":null}]}`, "/candidates/0/content"},
		{"missing-parts", `{"candidates":[{"content":{}}]}`, "/candidates/0/content/parts"},
		{"null-part", edit(`{"text":""}`, `null`), "/candidates/0/content/parts/0"},
		{"text-before-object", edit(`{"text":""}`, `{"text":null,"inlineData":null}`), "/candidates/0/content/parts/0/text"},
		{"empty-signature", edit(`{"text":""}`, `{"thoughtSignature":""}`), "/candidates/0/content/parts/0/thoughtSignature"},
		{"signature-only", edit(`{"text":""}`, `{"thoughtSignature":" "}`), acceptedFinalWire},
		{"unknown-only", edit(`{"text":""}`, `{"vendor":true}`), "/candidates/0/content/parts/0"},
		{"objects-before-signature", edit(`{"text":""}`, `{"inlineData":null,"fileData":null,"thoughtSignature":null}`), "/candidates/0/content/parts/0/inlineData"},
	}
	for _, field := range []string{"inlineData", "fileData", "functionCall", "functionResponse", "executableCode", "codeExecutionResult"} {
		cases = append(cases,
			finalWireCase{field + "-object", edit(`{"text":""}`, `{"`+field+`":{}}`), acceptedFinalWire},
			finalWireCase{field + "-null", edit(`{"text":""}`, `{"`+field+`":null}`), "/candidates/0/content/parts/0/" + field},
		)
	}
	return cases
}
