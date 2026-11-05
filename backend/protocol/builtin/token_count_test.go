package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageTokenCountingConversions(t *testing.T) {
	packages := map[string]*p.Compiled{}
	for _, name := range []string{"anthropic", "google-generate-content"} {
		packages[name] = compileBuiltinPackage(t, name)
	}
	for sourceName, source := range packages {
		document, _ := source.Package()
		decoder, _ := source.TokenCountRequestProtocol("count_tokens")
		for targetName, target := range packages {
			encoder, _ := target.TokenCountRequestProtocol("count_tokens")
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(decoder, encoder))
			if err != nil {
				t.Fatal(err)
			}
			for _, sample := range document.Mapping.TokenCountSamples {
				if sample.Direction != p.DecodeRequest || sample.ExpectedIssue != "" {
					continue
				}
				t.Run(sourceName+"/"+targetName+"/"+sample.ID, func(t *testing.T) {
					options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
					request, err := decoder.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
					if err != nil {
						t.Fatal(err)
					}
					route := p.ConversionContext{Source: decoder.Identity(), Target: encoder.Identity(), Scope: sample.Scope}
					request, err = conversion.Request(t.Context(), request, route, nil)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := encoder.EncodeRequestValue(t.Context(), request, options)
					if err != nil {
						t.Fatal(err)
					}
					if err := encoder.ValidateRequestOutput(t.Context(), wire, options); err != nil {
						t.Fatal(err)
					}
					decoded, err := encoder.DecodeRequestValue(t.Context(), wire, options)
					if err != nil {
						t.Fatal(err)
					}
					if !decoded.Parameters["max_output_tokens"].IsZero() || len(decoded.Content) != len(request.Content) || len(decoded.Tools) != len(request.Tools) {
						t.Fatal("counting changed its input or invented a generation budget", wire)
					}
					resultWire, err := target.EncodeTokenCount(t.Context(), "count_tokens", p.TokenCount{InputTokens: 9007199254740993})
					if err != nil {
						t.Fatal(err)
					}
					result, err := target.DecodeTokenCount(t.Context(), "count_tokens", resultWire)
					if err != nil || result.InputTokens != 9007199254740993 {
						t.Fatal("count result lost exact integer precision", result, err)
					}
				})
			}
		}
	}
}

func TestPackageTokenCountingRequestContracts(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body string
	}{
		{"anthropic", `{"model":"m","messages":[],"max_tokens":10}`},
		{"anthropic", `{"model":"m","messages":[],"stream":true}`},
		{"google-generate-content", `{"contents":[],"generateContentRequest":{"model":"models/m","contents":[]}}`},
		{"google-generate-content", `{"generateContentRequest":{"contents":[]}}`},
		{"google-generate-content", `{"generateContentRequest":{"model":"models/other","contents":[]}}`},
		{"google-generate-content", `{"contents":[],"tools":[]}`},
		{"google-generate-content", `{}`},
	} {
		t.Run(fixture.name+"/"+fixture.body, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.name)
			request, _ := compiled.TokenCountRequestProtocol("count_tokens")
			_, err := request.DecodeRequest(t.Context(), []byte(fixture.body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
			if err == nil {
				t.Fatal("invalid count request accepted", fixture.body)
			}
		})
	}
}
