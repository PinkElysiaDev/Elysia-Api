package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiPackageModalityAccounting(t *testing.T) {
	compiled := compileBlockPackage(t, "google-generate-content")
	for _, fixture := range []struct{ wire, prefix string }{{"promptTokensDetails", "input"}, {"candidatesTokensDetails", "output"}, {"cacheTokensDetails", "cache"}, {"toolUsePromptTokensDetails", "tool_input"}} {
		for _, modality := range []string{"TEXT", "IMAGE", "AUDIO", "VIDEO", "DOCUMENT"} {
			t.Run(fixture.wire+"/"+modality, func(t *testing.T) {
				raw := fmt.Sprintf(`{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"%s":[{"modality":"%s","tokenCount":9007199254740993}],"serviceTier":"standard"}}`, fixture.wire, modality)
				response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				name := fixture.prefix + "." + strings.ToLower(modality) + "_tokens"
				if response.Usage == nil || response.Usage.Details[name].Count != 9007199254740993 || response.Usage.Details[name].Origin != p.ObservedCount {
					t.Fatal("modality count lost precision or ownership", response.Usage)
				}
				delete(response.Usage.Details, name)
				response.Metadata = nil
				wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				if err != nil || strings.Contains(string(wire), fixture.wire) || strings.Contains(string(wire), "serviceTier") {
					t.Fatal("native snapshot restored removed core usage", string(wire), err)
				}
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer options.State.Release()
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options)
				if err != nil {
					t.Fatal(err)
				}
				hasCount := false
				for _, event := range frame.Events {
					if event.Unmapped != nil {
						t.Fatal("known accounting remained opaque")
					}
					if event.Usage != nil && event.Usage.Details[name].Count == 9007199254740993 {
						hasCount = true
					}
				}
				if !hasCount {
					t.Fatal("stream omitted modality count")
				}
			})
		}
	}
	for _, detail := range []string{`null`, `[{}]`, `[{"modality":"TEXT","tokenCount":-1}]`, `[{"modality":"TEXT","tokenCount":1.5}]`, `[{"modality":"TEXT","tokenCount":9223372036854775808}]`, `[{"modality":"TEXT","tokenCount":1},{"modality":"TEXT","tokenCount":1}]`} {
		if _, err := compiled.DecodeResponse(t.Context(), []byte(`{"usageMetadata":{"promptTokensDetails":`+detail+`}}`), fixtureOptions(p.EvaluationContext{})); err == nil {
			t.Fatal("invalid modality accounting accepted", detail)
		}
	}
}

func TestGeminiPackageNullableFinishAndMediaType(t *testing.T) {
	compiled := compileBlockPackage(t, "google-generate-content")
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for index, finish := range []string{"null", `"STOP"`} {
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":`+finish+`}]}`), options)
		if err != nil {
			t.Fatal(err)
		}
		hasTerminal := false
		for _, event := range frame.Events {
			if event.Type == p.ResponseFinished {
				hasTerminal = true
			}
		}
		if hasTerminal != (index == 1) {
			t.Fatal("null finish reason changed the lifecycle", frame.Events)
		}
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.ImageNode, Payload: testValue(t, `{"url":"https://example.org/image"}`)}}}}}
	if _, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{})); err == nil || !strings.Contains(err.Error(), "/mime") {
		t.Fatal("typed media without MIME was emitted with unverifiable semantics", err)
	}
	request.Content[0].Children[0].Payload = testValue(t, `{"url":"https://example.org/image","mime":"image/png"}`)
	wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := compiled.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
	if err != nil || decoded.Content[0].Children[0].Kind != p.ImageNode {
		t.Fatal("MIME-backed media kind was not preserved", decoded, err)
	}
	for _, role := range []string{`42`, `false`, `[]`, `{}`} {
		_, err := compiled.DecodeRequest(t.Context(), []byte(`{"systemInstruction":{"role":`+role+`,"parts":[{"text":"prefix"}]},"contents":[]}`), fixtureOptions(p.EvaluationContext{}))
		if err == nil || !strings.Contains(err.Error(), "/systemInstruction/role") {
			t.Fatal("ignored wrapper role bypassed wire validation", role, err)
		}
	}
}

func compileBlockPackage(t testing.TB, name string) *p.Compiled {
	t.Helper()
	raw, err := readPackage(name)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestBlockPackageMediaExtensionEdits(t *testing.T) {
	for _, fixture := range []struct{ name, body, owner, field string }{
		{"anthropic", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"YQ==","media_type":"image/png","vendor":[]}}]}]}`, "wire:claude", "source"},
		{"google-generate-content", `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"YQ==","vendor":{}}}]}]}`, "wire:gemini", "inlineData"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileBlockPackage(t, fixture.name)
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			node := &request.Content[0].Children[0]
			media, err := node.Payload.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			media["data"] = p.StringValue("Yg==")
			node.Payload, err = p.EncodeValue(media)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil || !strings.Contains(string(wire), `"vendor"`) || strings.Contains(string(wire), "YQ==") || !strings.Contains(string(wire), "Yg==") {
				t.Fatal("known media edit collided with its unknown sibling", string(wire), err)
			}
			node.Attributes[fixture.owner] = object(p.Object{fixture.field: object(p.Object{"data": p.StringValue("conflict")})})
			if _, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{})); err == nil {
				t.Fatal("extension overwrote a mapped media field")
			}
		})
	}
}

func TestBlockPackageUnknownMetadataMeaning(t *testing.T) {
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBlockPackage(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Source: p.Identity{Family: "unrelated", WireVersion: "1"}, Metadata: []p.ResponseMetadata{{Name: "field", Codec: "unrelated", Value: testValue(t, `1`), Path: "/provider/field"}}}
			_, err = conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, response.Source), nil)
			if err == nil || !strings.Contains(err.Error(), "/provider/field") || strings.Contains(err.Error(), "expected a string") {
				t.Fatal("unowned metadata lost its source diagnostic", err)
			}
		})
	}
}
