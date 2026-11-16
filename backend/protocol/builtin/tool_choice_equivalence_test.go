package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiSingletonRequiredToolChoiceIsNamedFunction(t *testing.T) {
	source := testCompiled(t, Gemini)
	decode := func(config string) *p.Request {
		t.Helper()
		request, err := source.DecodeRequest(t.Context(), []byte(`{"contents":[{"role":"user","parts":[{"text":"use the tool"}]}],"tools":[{"functionDeclarations":[{"name":"audit_echo","parameters":{"type":"object"}}]}],"toolConfig":`+config+`}`), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		request.Model = p.StringValue("m")
		return request
	}
	request := decode(`{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["audit_echo"]}}`)
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(name, func(t *testing.T) {
			target := testCompiled(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
			if err != nil {
				t.Fatal(err)
			}
			projected, err := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, source.Identity()), nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil || decoded.ToolChoice != request.ToolChoice || decoded.ToolChoice.IsZero() {
				t.Fatal("named function choice changed", string(wire), err)
			}
		})
	}
	for _, config := range []string{`{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["a","b"]}}`, `{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["a"]}}`} {
		request := decode(config)
		target := testCompiled(t, Chat)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, source.Identity()), nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("unequal allowed-tool constraint was discarded", config)
		}
	}
}
