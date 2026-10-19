package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type requestObservation struct {
	Wire, Error string
}

// Compare the public result of typed and JSON module dispatch, without freezing
// intermediate semantic representations or implementation-specific error chains.
func observeRequestPipeline(t *testing.T, compiled *p.Compiled, body []byte, options p.EvaluationContext) (result requestObservation) {
	t.Helper()
	request, err := compiled.DecodeRequest(t.Context(), body, options)
	if err == nil {
		conversion, compileErr := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
		if err == nil {
			request.Model = p.StringValue("actual-model")
			wire, encodeErr := compiled.EncodeRequest(t.Context(), request, options)
			result.Wire, err = string(wire), encodeErr
		}
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func BenchmarkRequestMemoryPipeline(b *testing.B) {
	fixtures := map[string]string{
		Chat:      `{"model":"m","messages":[{"role":"user","content":"hello"}]}`,
		Responses: `{"model":"m","input":"hello"}`,
		Anthropic: `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		Gemini:    `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		b.Run(codec, func(b *testing.B) {
			compiled := testCompiled(b, codec)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				b.Fatal(err)
			}
			body := []byte(fixtures[codec])
			options := p.EvaluationContext{Scope: p.Scope{Model: "m"}}
			b.ReportAllocs()
			for b.Loop() {
				request, err := compiled.DecodeRequest(b.Context(), body, options)
				if err != nil {
					b.Fatal(err)
				}
				request, err = conversion.Request(b.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := compiled.EncodeRequest(b.Context(), request, options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
