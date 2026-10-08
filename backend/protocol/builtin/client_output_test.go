package builtin

import (
	"encoding/json"
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// This is the unchanged shipped definition from 82406b9, not a fixture rebuilt
// from the new decoder's output. A custom copy changes only its public ID.
func TestLegacyCustomChatClientOutputCompatibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/chat-82406b9.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition p.Definition
	if err = json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	definition.ID = "old-custom-chat"
	raw, _ = json.Marshal(definition)
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	if report := p.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	parameters, err := compiled.BuildAgentParameters(t.Context(), p.AgentPreferences{MaxOutputTokens: 100, Stream: true})
	if err != nil || parameters["stream_options"].IsZero() {
		t.Fatal(parameters, err)
	}
	body := []byte(`{"model":"m","stream":true,"stream_options":{"include_usage":false,"future_flag":true},"messages":[{"role":"user","content":"hello"}]}`)
	request, err := compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if request.ClientOutput != nil || request.Parameters["stream_options"].IsZero() {
		t.Fatal("legacy contract changed", request)
	}
	request.Native = nil // Exercise the encoder, not native replay.
	wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, string(body))
	// Custom protocols keep this contract until a named conversion rule opts in.
	policy := p.ConversionPolicy{SchemaVersion: 1, ID: "opt-in", Rules: []p.ConversionRule{{ID: "client-preference", Order: 100, Enabled: true, Phase: p.ConversionRequest, Action: "stream_options"}}}
	conversion, err := p.CompileConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: p.Identity{Family: "gemini", WireVersion: "v2"}}, &p.DiagnosticSink{})
	if err != nil {
		t.Fatal(err)
	}
	if !converted.Parameters["stream_options"].IsZero() || converted.ClientOutput == nil || converted.ClientOutput.IncludeUsage == nil || *converted.ClientOutput.IncludeUsage || !converted.ClientOutput.RawStreamOptions.IsZero() {
		t.Fatal("legacy preference was not normalized", converted)
	}
	if request.Parameters["stream_options"].IsZero() {
		t.Fatal("candidate mutated original request")
	}
}

func TestVersionedChatClientOutputContract(t *testing.T) {
	for _, declarativePath := range []bool{false, true} {
		compiled := compileAgentDefinition(t, func(d *p.Definition) {
			if declarativePath {
				m := d.Directions[p.DecodeRequest]
				m.After = &p.Expression{Op: "read", Path: ""}
				d.Directions[p.DecodeRequest] = m
			}
		})
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[]}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if request.ClientOutput == nil || request.ClientOutput.IncludeUsage == nil || !*request.ClientOutput.IncludeUsage || !request.Parameters["stream_options"].IsZero() {
			t.Fatal("new contract not selected", request)
		}
	}
}
