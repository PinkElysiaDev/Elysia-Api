package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func compileAgentDefinition(t *testing.T, mutate func(*p.Definition)) *p.Compiled {
	t.Helper()
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range definitions {
		var definition p.Definition
		if err := value.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		if definition.ID != "openai-chat-completions" {
			continue
		}
		mutate(&definition)
		compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.Compile(raw)
		if compiled == nil {
			t.Fatal(issues)
		}
		return compiled
	}
	t.Fatal("missing Chat definition")
	return nil
}

func TestAgentPolicyRequiresTransportEffortAndIndependentEvidence(t *testing.T) {
	compiled := compileAgentDefinition(t, func(definition *p.Definition) { definition.ID = "user-authored-copy" })
	if report := p.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	preferences := p.AgentPreferences{MaxOutputTokens: 4096, Stream: true}
	parameters, err := compiled.BuildAgentParameters(t.Context(), preferences)
	if err != nil || string(parameters["stream"].Bytes()) != "true" || !parameters["reasoning_effort"].IsZero() {
		t.Fatal(parameters, err)
	}
	preferences.ThinkingEnabled, preferences.ThinkingEffort = true, "undeclared"
	if _, err := compiled.BuildAgentParameters(t.Context(), preferences); err == nil {
		t.Fatal("unknown effort silently defaulted")
	}
	withoutPolicy := compileAgentDefinition(t, func(definition *p.Definition) { definition.Agent = nil })
	if _, err := withoutPolicy.BuildAgentParameters(t.Context(), preferences); err == nil {
		t.Fatal("missing policy accepted")
	}
	for _, scenario := range []string{"missing_stream", "mismatched_expectation", "wire_parameter_loss", "tool_result_loss"} {
		t.Run(scenario, func(t *testing.T) {
			broken := compileAgentDefinition(t, func(definition *p.Definition) {
				switch scenario {
				case "missing_stream":
					var samples []p.AgentSample
					for _, sample := range definition.Agent.Samples {
						if !sample.Preferences.Stream {
							samples = append(samples, sample)
						}
					}
					definition.Agent.Samples = samples
				case "mismatched_expectation":
					definition.Agent.Samples[0].Expected = testValue(t, `{"max_output_tokens":1}`)
				case "wire_parameter_loss", "tool_result_loss":
					mapping := definition.Directions[p.EncodeRequest]
					fields := map[string]p.Expression{}
					for _, name := range []string{"model", "messages", "tools", "max_completion_tokens", "stream", "stream_options", "reasoning_effort"} {
						fields[name] = p.Expression{Op: "read", Path: "/" + name}
					}
					if scenario == "wire_parameter_loss" {
						delete(fields, "max_completion_tokens")
					} else {
						fields["messages"] = p.Expression{Op: "literal", Value: testValue(t, `[]`)}
					}
					mapping.After = &p.Expression{Op: "object", Fields: fields}
					definition.Directions[p.EncodeRequest] = mapping
				}
			})
			if report := p.Verify(t.Context(), broken); report.Passed {
				t.Fatal("lossy Agent policy verified")
			}
			if broken.SamplesHash() == compiled.SamplesHash() && scenario == "missing_stream" {
				t.Fatal("Agent evidence omitted from sample hash")
			}
		})
	}
}

func TestAgentRequestPreparesOnlyLocalResultsWithoutMutatingHistory(t *testing.T) {
	compiled := compileAgentDefinition(t, func(*p.Definition) {})
	request := p.Request{SchemaVersion: 1, Source: p.AgentIdentity(), Model: p.StringValue("m"), Content: []p.Node{
		{Kind: p.ToolResultNode, CallID: p.StringValue("c"), Payload: testValue(t, `{"n":9007199254740993,"zero":0,"empty":null}`), Source: &p.Provenance{Protocol: p.AgentIdentity()}},
		{Kind: p.ReasoningNode, Payload: p.StringValue("thinking"), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("sig"), Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}}}},
	}}
	before, _ := p.EncodeValue(request)
	prepared, err := compiled.BuildAgentRequest(context.Background(), request, p.AgentPreferences{MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := p.EncodeValue(request)
	if string(before.Bytes()) != string(after.Bytes()) {
		t.Fatal("Agent preparation mutated persisted content")
	}
	var text string
	if err := prepared.Content[0].Payload.Decode(&text); err != nil || !strings.Contains(text, "9007199254740993") {
		t.Fatal(text, err)
	}
	if prepared.Content[1].Resources[0] != request.Content[1].Resources[0] {
		t.Fatal("provider scope was rewritten")
	}
	request.Source = compiled.Identity()
	if _, err := compiled.BuildAgentRequest(t.Context(), request, p.AgentPreferences{MaxOutputTokens: 100}); err == nil {
		t.Fatal("provider identity accepted as local Agent author")
	}
}
