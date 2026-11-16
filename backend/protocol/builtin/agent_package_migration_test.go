package builtin

import (
	"os"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageAgentAndModelDiscoveryMigration(t *testing.T) {
	raw, err := os.ReadFile("testdata/agent-discovery-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name   string          `json:"name"`
		Models []p.ModelSample `json:"models"`
		Agent  []p.AgentSample `json:"agent"`
	}
	if err := testValue(t, string(raw)).Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.Name)
			for _, sample := range fixture.Models {
				t.Run(sample.ID, func(t *testing.T) {
					page, err := compiled.DecodeModelPage(t.Context(), sample.Operation, sample.Input)
					if err != nil || !equalAgentMigrationValues(t, page, sample.Expected) {
						t.Fatal("model catalog changed its independent baseline expectation", page, sample.Expected, err)
					}
				})
			}
			for _, sample := range fixture.Agent {
				t.Run(sample.ID, func(t *testing.T) {
					checkAgentRequestContract(t, compiled, fixture.Name, sample)
				})
			}
		})
	}
}

func checkAgentRequestContract(t *testing.T, compiled *p.Compiled, name string, sample p.AgentSample) {
	t.Helper()
	preferences := sample.Preferences
	wantParameters := p.Object{"max_output_tokens": testValue(t, "8192")}
	reasoning := p.ReasoningIntent{Mode: "disabled"}
	if preferences.ThinkingEnabled {
		effort := preferences.ThinkingEffort
		if effort == "" || effort == "adaptive" {
			effort = "medium"
		}
		reasoning = p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue(effort), EffortScope: "reasoning"}
		switch name {
		case "anthropic":
			reasoning.Summary = p.StringValue("summarized")
		case "openai-responses", "google-generate-content":
			reasoning.Summary = p.StringValue("auto")
		}
	}
	wantParameters["reasoning"], _ = p.EncodeValue(reasoning)
	if name != "google-generate-content" {
		wantParameters["stream"], _ = p.EncodeValue(preferences.Stream)
	}
	parameters, err := compiled.BuildAgentParameters(t.Context(), preferences)
	if err != nil || !equalAgentMigrationValues(t, parameters, wantParameters) {
		t.Fatal("Agent parameters differ from the preference contract", parameters, wantParameters, err)
	}
	request := p.Request{SchemaVersion: p.ProtocolCoreVersion, Source: p.AgentIdentity(), Model: p.StringValue(sample.Model), Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}},
		{Kind: p.ToolResultNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Payload: testValue(t, `{"n":9007199254740993,"empty":null}`), Source: &p.Provenance{Protocol: p.AgentIdentity()}},
	}}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := compiled.BuildAgentRequest(t.Context(), request, preferences)
	if err != nil {
		t.Fatal(err)
	}
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("Agent preparation mutated its source history", err)
	}
	wantOutput := &p.ClientOutput{CollectUsage: preferences.Stream, ResponsesStorage: &p.ResponsesStorageIntent{}, RequireContinuationState: name == "openai-responses"}
	if !equalAgentMigrationValues(t, prepared.ClientOutput, wantOutput) || !equalAgentMigrationValues(t, prepared.Reasoning, reasoning) {
		t.Fatal("Agent preparation changed client intent or thinking controls", prepared)
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := conversion.Request(t.Context(), prepared, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: sample.Model}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: sample.Model}, Values: p.Object{"model": p.StringValue(sample.Model)}, ClientOutput: projected.ClientOutput})
	wire, err := compiled.EncodeRequest(t.Context(), projected, options)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := compiled.DecodeRequest(t.Context(), wire, options)
	if err != nil || decoded.Parameters["max_output_tokens"].JSONString() != "8192" {
		t.Fatal("Agent wire lost the selected output limit", string(wire), err)
	}
	if !equalAgentMigrationValues(t, projected.Reasoning, decoded.Reasoning) {
		t.Fatal("Agent wire changed projected thinking controls", projected.Reasoning, decoded.Reasoning)
	}
	calls, results := fixtureNodes(decoded.Content, p.ToolCallNode), fixtureNodes(decoded.Content, p.ToolResultNode)
	if len(calls) != 1 || len(results) != 1 || !calls[0].CallID.IsString("call") || !results[0].CallID.IsString("call") || !calls[0].Name.IsString("lookup") || calls[0].Input == nil || !equivalentJSON(calls[0].Input.Value, testValue(t, `{"n":9007199254740993}`)) {
		t.Fatal("Agent wire changed tool identity or arguments", string(wire))
	}
	payload := results[0].Payload
	if payload.IsStringValue() {
		var result string
		if err := payload.Decode(&result); err != nil {
			t.Fatal(err)
		}
		payload = testValue(t, result)
	}
	if !equivalentJSON(payload, testValue(t, `{"n":9007199254740993,"empty":null}`)) {
		t.Fatal("Agent wire changed the exact tool result", payload)
	}
}

func equalAgentMigrationValues(t *testing.T, left, right any) bool {
	t.Helper()
	decode := func(input any) any {
		t.Helper()
		value, err := p.EncodeValue(input)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := value.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	return reflect.DeepEqual(decode(left), decode(right))
}
