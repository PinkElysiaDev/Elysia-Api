package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func agentFixturePackage(t testing.TB) p.ProtocolPackage {
	t.Helper()
	raw, err := readPackage("openai-chat")
	if err != nil {
		t.Fatal(err)
	}
	var document p.ProtocolPackage
	if err := raw.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func compilePackageDocument(t testing.TB, document p.ProtocolPackage) (*p.Compiled, []p.ConversionIssue) {
	t.Helper()
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	return compiler.Compile(raw.Bytes())
}

func TestAgentPolicyRequiresTransportEffortAndIndependentEvidence(t *testing.T) {
	document := agentFixturePackage(t)
	document.Spec.ID = "user-authored-copy"
	compiled, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	preferences := p.AgentPreferences{MaxOutputTokens: 4096, Stream: true}
	parameters, err := compiled.BuildAgentParameters(t.Context(), preferences)
	if err != nil || parameters["stream"] != testValue(t, "true") || !parameters["reasoning_effort"].IsZero() {
		t.Fatal(parameters, err)
	}
	preferences.ThinkingEnabled, preferences.ThinkingEffort = true, "undeclared"
	if _, err := compiled.BuildAgentParameters(t.Context(), preferences); err == nil {
		t.Fatal("unknown effort silently defaulted")
	}
	withoutPolicy := agentFixturePackage(t)
	withoutPolicy.Mapping.Agent = nil
	missing, issues := compilePackageDocument(t, withoutPolicy)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if _, err := missing.BuildAgentParameters(t.Context(), preferences); err == nil {
		t.Fatal("missing Agent policy accepted")
	}
	for _, scenario := range []string{"missing_stream", "mismatched_expectation", "wire_parameter_loss", "tool_result_loss"} {
		t.Run(scenario, func(t *testing.T) {
			broken := agentFixturePackage(t)
			switch scenario {
			case "missing_stream":
				var samples []p.AgentSample
				for _, sample := range broken.Mapping.Agent.Samples {
					if !sample.Preferences.Stream {
						samples = append(samples, sample)
					}
				}
				broken.Mapping.Agent.Samples = samples
			case "mismatched_expectation":
				broken.Mapping.Agent.Samples[0].Expected = testValue(t, `{"max_output_tokens":1}`)
			case "wire_parameter_loss", "tool_result_loss":
				mapping := broken.Mapping.Directions[p.EncodeRequest]
				field := "max_completion_tokens"
				if scenario == "tool_result_loss" {
					field = "messages"
				}
				program := mapping.Program
				mapping.Program = p.Expression{Op: "project", Source: &program, Keys: []string{field}, Policy: "exclude"}
				broken.Mapping.Directions[p.EncodeRequest] = mapping
			}
			candidate, issues := compilePackageDocument(t, broken)
			if p.IssuesError(issues) != nil {
				return
			}
			if report := p.Verify(t.Context(), candidate); report.Passed {
				t.Fatal("lossy Agent policy verified")
			}
			if scenario == "missing_stream" && candidate.SamplesHash() == compiled.SamplesHash() {
				t.Fatal("Agent evidence omitted from the sample hash")
			}
		})
	}
}

func TestAgentRequestPreparesOnlyLocalResultsWithoutMutatingHistory(t *testing.T) {
	compiled := testCompiled(t, Chat)
	request := p.Request{SchemaVersion: p.ProtocolCoreVersion, Source: p.AgentIdentity(), Model: p.StringValue("m"), Content: []p.Node{
		{Kind: p.ToolResultNode, CallID: p.StringValue("c"), Payload: testValue(t, `{"n":9007199254740993,"zero":0,"empty":null}`), Source: &p.Provenance{Protocol: p.AgentIdentity()}},
		{Kind: p.ReasoningNode, Payload: p.StringValue("thinking"), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("sig"), Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}}}},
	}}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := compiled.BuildAgentRequest(t.Context(), request, p.AgentPreferences{MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("Agent preparation mutated persisted content", err)
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
		t.Fatal("provider identity accepted as a local Agent author")
	}
}
