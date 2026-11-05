package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type requestObservation struct {
	Wire, Error string
}

func BenchmarkAnthropicGeminiProjection(b *testing.B) {
	for _, size := range []int{64, 2 << 20} {
		name := "short"
		if size > 64 {
			name = "history2MiB"
		}
		b.Run(name, func(b *testing.B) {
			source, target := testCompiled(b, Anthropic), testCompiled(b, Gemini)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
			if err != nil {
				b.Fatal(err)
			}
			body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"` + strings.Repeat("h", size) + `"}]}`)
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			b.ReportAllocs()
			for b.Loop() {
				request, err := source.DecodeRequest(b.Context(), body, p.EvaluationContext{})
				if err != nil {
					b.Fatal(err)
				}
				request, err = conversion.Request(b.Context(), request, route, nil)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := target.EncodeRequest(b.Context(), request, p.EvaluationContext{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
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
