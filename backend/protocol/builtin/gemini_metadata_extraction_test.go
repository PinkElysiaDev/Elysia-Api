package builtin

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	geminiMetadataTime = `"2026-11-14T12:00:00.123456Z"`
	geminiMetadataBody = `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`
)

func buildGeminiMetadataResponse(value string) string {
	if value == "" {
		return geminiMetadataBody
	}
	return strings.TrimSuffix(geminiMetadataBody, "}") + `,"createTime":` + value + `}`
}

func editGeminiCreateTime(metadata []p.ResponseMetadata) {
	for index := range metadata {
		if metadata[index].Name == "createTime" {
			metadata[index].Value = p.StringValue("edited-time")
			metadata[index].Meaning = "information"
		}
	}
}

func requireGeminiCreateTime(t *testing.T, response *p.Response, value string) {
	t.Helper()
	count := 0
	for _, field := range response.Metadata {
		if field.Name != "createTime" {
			continue
		}
		count++
		meaning := "information"
		if value == "null" {
			meaning = "empty"
		}
		if field.Location != "response" || field.Codec != Gemini || field.SourceCodec != Gemini || field.Path != "/createTime" || field.Meaning != meaning || field.Value != testValue(t, value) {
			t.Fatal("createTime lost its response owner, value or meaning", field)
		}
	}
	if count != 1 || !response.Attributes["wire:gemini"].IsZero() {
		t.Fatal("createTime must have exactly one metadata owner", response.Metadata, response.Attributes)
	}
}

func TestGeminiMetadataExtractionRebuild(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	for _, value := range []string{"", "null", geminiMetadataTime} {
		t.Run(value, func(t *testing.T) {
			body := buildGeminiMetadataResponse(value)
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if value == "" {
				if len(response.Metadata) != 0 {
					t.Fatal("missing createTime produced metadata", response.Metadata)
				}
			} else {
				requireGeminiCreateTime(t, response, value)
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			if value == "" {
				return
			}
			editGeminiCreateTime(response.Metadata)
			wire, err = compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, buildGeminiMetadataResponse(`"edited-time"`))
			response.Attributes["wire:gemini"] = testValue(t, `{"createTime":"conflict"}`)
			if _, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
				t.Fatal("wire remainder overwrote edited createTime")
			}
		})
	}
}

func TestGeminiMetadataExtractionStreamParity(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	_, baseline := decodePackageStream(t, compiled, []string{geminiMetadataBody}, p.Scope{})
	for _, value := range []string{"null", geminiMetadataTime} {
		body := buildGeminiMetadataResponse(value)
		response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		frames, collected := decodePackageStream(t, compiled, []string{body}, p.Scope{})
		requireGeminiCreateTime(t, collected, value)
		if !reflect.DeepEqual(response.Metadata, collected.Metadata) || !reflect.DeepEqual(baseline.Usage, collected.Usage) {
			t.Fatal("stream changed metadata ownership or model usage", response.Metadata, collected.Metadata, collected.Usage)
		}
		for _, frame := range frames {
			for index := range frame.Events {
				event := &frame.Events[index]
				event.Native = nil
				if event.Unmapped != nil {
					t.Fatal("known createTime remained unmapped", event)
				}
				if event.Response != nil {
					event.Response.Native = nil
				}
			}
		}
		rebuilt := encodePackageStream(t, compiled, frames, p.Scope{}, false)
		_, roundTrip := decodePackageStream(t, compiled, rebuilt, p.Scope{})
		requireGeminiCreateTime(t, roundTrip, value)
		if !reflect.DeepEqual(collected.Usage, roundTrip.Usage) {
			t.Fatal("metadata reconstruction changed usage", collected.Usage, roundTrip.Usage)
		}
		for _, frame := range frames {
			for _, event := range frame.Events {
				editGeminiCreateTime(event.Metadata)
				if event.Response != nil {
					editGeminiCreateTime(event.Response.Metadata)
				}
			}
		}
		rebuilt = encodePackageStream(t, compiled, frames, p.Scope{}, false)
		_, edited := decodePackageStream(t, compiled, rebuilt, p.Scope{})
		requireGeminiCreateTime(t, edited, `"edited-time"`)
	}
}

func TestGeminiMetadataExtractionSpecRejectsInvalidTypes(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	for _, value := range []string{"1", `{}`, `[]`, "false"} {
		body := buildGeminiMetadataResponse(value)
		_, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		requireGeminiMetadataIssue(t, err, p.UpstreamContractViolation)
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		_, err = compiled.DecodeFrame(t.Context(), testValue(t, body), options)
		options.State.Release()
		requireGeminiMetadataIssue(t, err, p.UpstreamContractViolation)
		requireGeminiMetadataIssue(t, compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, body)), p.ConversionRejected)
	}
}

func requireGeminiMetadataIssue(t *testing.T, err error, code p.IssueCode) {
	t.Helper()
	var failure *p.ConversionError
	if errors.As(err, &failure) {
		for _, issue := range failure.Issues {
			if issue.Path == "/createTime" && issue.Code == code {
				return
			}
		}
	}
	t.Fatal("invalid createTime lost its wire-boundary classification", code, err)
}

func TestGeminiMetadataExtractionProjection(t *testing.T) {
	source := testCompiled(t, Gemini)
	for _, name := range []string{Chat, Responses, Anthropic} {
		target := testCompiled(t, name)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			for _, value := range []string{"null", geminiMetadataTime} {
				t.Run(name+"/"+mode+"/"+value, func(t *testing.T) {
					body := buildGeminiMetadataResponse(value)
					response, err := source.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					conversion := resolveMetadataExtractionConversion(t, source, target, p.ConversionResponse, mode)
					route := fixtureConversionContext(t, conversion, p.ConversionResponse, source.Identity())
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Response(t.Context(), response, route, sink)
					if err == nil {
						_, err = target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
					}
					shouldPass := mode == "compatible" || mode == "strict" && value == "null"
					if (err == nil) != shouldPass {
						t.Fatal("metadata projection ignored its declared policy", err)
					}
					if shouldPass {
						code := p.ConversionDegraded
						if value == "null" {
							code = p.ConversionNormalized
						}
						hasDiagnostic := false
						for _, issue := range sink.Issues() {
							if issue.Path == "/createTime" && issue.Code == code && issue.RuleID == "response-metadata" && issue.PolicyHash == conversion.Hash {
								hasDiagnostic = true
							}
						}
						if !hasDiagnostic {
							t.Fatal("createTime projection lost its diagnostic", sink.Issues())
						}
					}
					requireGeminiCreateTime(t, response, value)
				})
			}
		}
		convertFixtureStream(t, source, target, []string{buildGeminiMetadataResponse(geminiMetadataTime)})
	}
}

func TestGeminiMetadataExtractionStreamPolicies(t *testing.T) {
	source := testCompiled(t, Gemini)
	frames, _ := decodePackageStream(t, source, []string{buildGeminiMetadataResponse(geminiMetadataTime)}, p.Scope{})
	for _, name := range []string{Chat, Responses, Anthropic} {
		target := testCompiled(t, name)
		for _, mode := range []string{"strict", "disabled"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				conversion := resolveMetadataExtractionConversion(t, source, target, p.ConversionEvent, mode)
				route, err := conversion.WithFrameUsage(conversion.VerificationRoute(source.Identity(), target.Identity(), p.SSE), frames[0].Events)
				if err != nil {
					t.Fatal(err)
				}
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}).WithConversion(conversion, route)
				defer options.State.Release()
				for _, event := range frames[0].Events {
					projected, projectErr := conversion.Event(t.Context(), event, route, nil)
					err = projectErr
					if err == nil {
						_, err = target.EncodeFrames(t.Context(), projected, options)
					}
					if err != nil {
						break
					}
				}
				requireDraftRejection(t, err, "metadata")
			})
		}
	}
}

func TestGeminiMetadataExtractionPreservesUnknownNeighbor(t *testing.T) {
	source := testCompiled(t, Gemini)
	body := strings.TrimSuffix(buildGeminiMetadataResponse(geminiMetadataTime), "}") + `,"futureTime":{"n":9007199254740993}}`
	response, err := source.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	wire, err := source.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, body)
	for _, name := range []string{Chat, Responses, Anthropic} {
		target := testCompiled(t, name)
		conversion := resolveMetadataExtractionConversion(t, source, target, p.ConversionResponse, "compatible")
		projected, err := conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, source.Identity()), nil)
		if err == nil {
			_, err = target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("unknown neighboring field crossed its source owner", name)
		}
	}
}
