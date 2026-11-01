package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageInstructionLayout(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-chat")
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"first"},{"role":"system","content":"policy"},{"role":"developer","content":"detail"},{"role":"user","content":"last"}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			policy := p.DefaultConversionPolicy(source, target)
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, route, sink)
			if err != nil || len(projected.Content) != 3 || projected.Native != nil || len(projected.Content[0].Children) != 2 {
				t.Fatal("instruction layout failed", projected, err)
			}
			if projected.Content[0].Children[0].Payload != request.Content[1].Children[0].Payload || projected.Content[0].Children[1].Payload != request.Content[2].Children[0].Payload {
				t.Fatal("explicit root combination changed child order", projected.Content[0])
			}
			for index, original := range []int{0, 3} {
				node := projected.Content[index+1]
				if node.Children[0].Payload != request.Content[original].Children[0].Payload || node.Source == nil || *node.Source != *request.Content[original].Source {
					t.Fatal("layout changed content order or provenance", projected.Content)
				}
			}
			var issues []p.ConversionIssue
			for _, issue := range sink.Issues() {
				if issue.RuleID == "system-instruction-hoist" {
					issues = append(issues, issue)
				}
			}
			if len(issues) != 3 || issues[0].Path != "/content/1" || issues[1].Path != "/content/2" || issues[2].Path != "/content/1" || issues[0].PolicyHash != conversion.Hash {
				t.Fatal("layout lost its explicit scope or boundary diagnostics", issues)
			}
			wire, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
			if err != nil || len(decoded.Content) != 3 || !decoded.Content[0].Role.IsString("system") || len(decoded.Content[0].Children) != 2 || !decoded.Content[0].Children[0].Payload.IsString("policy") || !decoded.Content[0].Children[1].Payload.IsString("detail") || !decoded.Content[1].Children[0].Payload.IsString("first") || !decoded.Content[2].Children[0].Payload.IsString("last") {
				t.Fatal("target did not preserve the projected instruction order", string(wire), err)
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := strict.Request(t.Context(), request, route, nil); err == nil {
				t.Fatal("strict mode accepted changed instruction scope")
			}
			policy.Mode = "compatible"
			for index := range policy.Rules {
				if policy.Rules[index].ID == "system-instruction-hoist" {
					policy.Rules[index].Enabled = false
				}
			}
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := disabled.Request(t.Context(), request, route, nil)
			if err != nil || !unchanged.Content[0].Role.IsString("user") {
				t.Fatal("disabled layout still ran", err)
			}
			if _, err := target.EncodeRequest(t.Context(), unchanged, p.EvaluationContext{}); err == nil {
				t.Fatal("encoder repeated the disabled instruction layout")
			}
			prefix := request.Clone()
			prefix.Native = nil
			prefix.Content = prefix.Content[1:3]
			prefix.Content[1].Role = p.StringValue("system")
			if _, err := target.EncodeRequest(t.Context(), prefix, p.EvaluationContext{}); err == nil {
				t.Fatal("encoder combined instruction roots without the declared layout")
			}
			withMetadata := request.Clone()
			withMetadata.Content[1].Attributes = p.Object{"wire:future": p.StringValue("retained")}
			if _, err := conversion.Request(t.Context(), withMetadata, route, nil); err == nil {
				t.Fatal("layout dropped instruction container attributes")
			}
		})
	}
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("instruction layout changed caller-owned input", err)
	}
}

func TestPackageInstructionLayoutPreservesNativePlacement(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	const raw = `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"first"},{"role":"system","content":"inline"}]}`
	request, err := compiled.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(compiled, compiled)
	policy.Mode = "strict"
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, sink)
	if err != nil || projected.Native == nil || len(sink.Issues()) != 0 {
		t.Fatal("documented native placement was changed", err, sink.Issues())
	}
	wire, err := compiled.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, raw)
	request.Native, request.Content[1].Native = nil, nil
	if _, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil); err == nil {
		t.Fatal("source annotation authorized inline placement without its native container")
	}
}
