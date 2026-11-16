package builtin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	responseRetentionUsage     = `"usage":{"input_tokens":12000,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}},"output_tokens":5,"total_tokens":12005}`
	responseZeroRetentionUsage = `"usage":{"input_tokens":3,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":0}},"output_tokens":1,"total_tokens":4}`
	responseTransformation     = `[{"id":"source-2","type":"reasoning","action":"removed","reason":"model_mismatch"},{"id":"source-1","type":"reasoning","action":"removed","reason":"turn_boundary"}]`
	coreTransformation         = `[{"sourceItemId":"source-2","kind":"reasoning","action":"remove","reason":"model-mismatch"},{"sourceItemId":"source-1","kind":"reasoning","action":"remove","reason":"turn-boundary"}]`
)

func readResponseExtensionFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/responses-live-extensions." + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func replaceResponseFrameFields(t *testing.T, frame, fields string) string {
	t.Helper()
	var envelope, response, updates map[string]json.RawMessage
	if err := json.Unmarshal([]byte(frame), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope["response"], &response); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte("{"+fields+"}"), &updates); err != nil {
		t.Fatal(err)
	}
	for name, value := range updates {
		response[name] = value
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	envelope["response"] = encoded
	encoded, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func resolveResponseExtensionConversion(t *testing.T, from, to *p.Compiled, phase p.ConversionPhase, mode, rule string) *p.CompiledConversion {
	t.Helper()
	policy := p.DefaultConversionPolicy(to, from)
	if mode == "strict" {
		policy.Mode = mode
	}
	if mode == "disabled" {
		for index := range policy.Rules {
			if policy.Rules[index].ID == string(phase)+"-"+rule {
				policy.Rules[index].Enabled = false
			}
		}
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	return conversion
}

func TestResponseExtensionPackagesCompile(t *testing.T) {
	for _, codec := range []string{Responses, Chat, Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) { shippedProjectionProtocol(t, codec) })
	}
}

func TestResponsesExtensionObservationRoundTrips(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, wire, canonical, meaning string }{
		{"image", `{"context_compression":"disabled"}`, "false", "image-context-compression"},
		{"image", `{"context_compression":"enabled"}`, "true", "image-context-compression"},
		{"image", "null", "null", "image-context-compression"},
		{"input_transformations", "[]", "[]", "input-transformations"},
		{"input_transformations", "null", "null", "input-transformations"},
		{"input_transformations", responseTransformation, coreTransformation, "input-transformations"},
	} {
		t.Run(fixture.name+"/"+fixture.wire, func(t *testing.T) {
			body := buildResponseMetadataBody(fmt.Sprintf("%q:%s", fixture.name, fixture.wire))
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			requireResponseMetadata(t, response.Metadata, fixture.name, fixture.canonical, fixture.meaning, "")
			response.Native = nil
			clearMediaScopeNative(response.Content)
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			if fixture.name == "image" && fixture.canonical == "false" {
				for index := range response.Metadata {
					if response.Metadata[index].Name == "image" {
						response.Metadata[index].Value = testValue(t, "true")
					}
				}
				wire, err = compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, wire, strings.Replace(body, `"disabled"`, `"enabled"`, 1))
			}
		})
	}
}

func TestResponsesExtensionPolicyBoundaries(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, fixture := range []struct {
			name, fields               string
			canNormalize, mustPreserve bool
		}{
			{"disabled-image", `"image":{"context_compression":"disabled"}`, false, false},
			{"enabled-image", `"image":{"context_compression":"enabled"}`, false, false},
			{"unreported-image", `"image":null`, false, true},
			{"empty-transformations", `"input_transformations":[]`, true, false},
			{"unreported-transformations", `"input_transformations":null`, false, true},
			{"source-transformations", `"input_transformations":` + responseTransformation, false, true},
		} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(target+"/"+fixture.name+"/"+mode, func(t *testing.T) {
					response, err := from.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fixture.fields)), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					response.Native = nil
					delete(response.Attributes, "created_at")
					response.Content[0].ID, response.Content[0].Status = p.Value{}, p.Value{}
					response.Usage = &p.Usage{Input: &p.Counter{Count: 3, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}, Total: &p.Counter{Count: 4, Origin: p.ObservedCount}}
					before, _ := p.EncodeValue(response)
					conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionResponse, mode, "metadata")
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), sink)
					if err == nil {
						_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
					}
					shouldReject := fixture.mustPreserve || mode == "disabled" || mode == "strict" && !fixture.canNormalize
					if shouldReject {
						requireDraftRejection(t, err, "")
					} else {
						if err != nil {
							t.Fatal(err)
						}
						code := p.ConversionDegraded
						if fixture.canNormalize {
							code = p.ConversionNormalized
						}
						hasDiagnostic := false
						for _, issue := range sink.Issues() {
							if (issue.Path == "/image" || issue.Path == "/input_transformations") && issue.Code == code && issue.RuleID == "response-metadata" && issue.PolicyHash == conversion.Hash {
								hasDiagnostic = true
							}
						}
						if !hasDiagnostic {
							t.Fatal("observation projection lost its path, loss class or policy identity", sink.Issues())
						}
					}
					after, _ := p.EncodeValue(response)
					if before.JSONString() != after.JSONString() {
						t.Fatal("target projection mutated the source observation")
					}
				})
			}
		}
	}
}

func TestResponsesRetentionRoundTripAndProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, fields := range []string{responseRetentionUsage, responseZeroRetentionUsage,
		`"usage":{"input_tokens":9007199254740993,"input_tokens_details":{"cache_write_tokens":9007199254740993,"cache_write_tokens_details":{"ttl_30m_tokens":9007199254740993,"ttl_12h_tokens":0}},"output_tokens":0,"total_tokens":9007199254740993}`} {
		body := buildResponseMetadataBody(fields)
		response, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"cache_creation.30m_tokens", "cache_creation.12h_tokens"} {
			if counter, hasCounter := response.Usage.Details[name]; !hasCounter || counter.Origin != p.ObservedCount {
				t.Fatal("retention presence or observed zero was lost", response.Usage)
			}
		}
		response.Native = nil
		clearMediaScopeNative(response.Content)
		wire, err := from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		// Keep strict usage assertions independent of item-layout and timestamp loss.
		delete(response.Attributes, "created_at")
		response.Content[0].ID, response.Content[0].Status = p.Value{}, p.Value{}
		for _, target := range []string{Chat, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionResponse, mode, "usage-projection")
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), sink)
				if err == nil {
					_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
				}
				if mode != "compatible" {
					requireDraftRejection(t, err, "usage")
					continue
				}
				if err != nil {
					t.Fatal(target, err)
				}
				for _, name := range []string{"cache_creation.30m_tokens", "cache_creation.12h_tokens"} {
					if _, hasCounter := projected.Usage.Details[name]; hasCounter {
						t.Fatal("target retained an unsupported duration detail", target)
					}
					hasDiagnostic := false
					for _, issue := range sink.Issues() {
						if issue.Path == "/usage/details/"+name && issue.Code == p.ConversionDegraded && issue.RuleID == "response-usage-projection" && issue.PolicyHash == conversion.Hash {
							hasDiagnostic = true
						}
					}
					if !hasDiagnostic {
						t.Fatal("target lost duration detail without its declared diagnostic", sink.Issues())
					}
					if _, hasCounter := response.Usage.Details[name]; !hasCounter {
						t.Fatal("client projection removed the source accounting detail")
					}
				}
				if *projected.Usage.Input != *response.Usage.Input || *projected.Usage.Output != *response.Usage.Output || *projected.Usage.Total != *response.Usage.Total {
					t.Fatal("duration detail was billed again or changed model totals")
				}
			}
		}
		delete(response.Usage.Details, "cache_creation.12h_tokens")
		_, err = from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		requireDraftRejection(t, err, "usage")
	}
}

func TestResponsesExtensionWireRejections(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fields := range []string{
		`"image":false`, `"image":{}`, `"image":{"context_compression":"auto"}`,
		`"image":{"context_compression":"disabled","future":true}`,
		`"input_transformations":{}`, `"input_transformations":[{"id":"","type":"reasoning","action":"removed","reason":"model_mismatch"}]`,
		`"input_transformations":[{"id":"r","type":"text","action":"removed","reason":"model_mismatch"}]`,
		`"input_transformations":[{"id":"r","type":"reasoning","action":"remove","reason":"model_mismatch"}]`,
		`"input_transformations":[{"id":"r","type":"reasoning","action":"removed","reason":"future"}]`,
		`"input_transformations":[{"id":"r","type":"reasoning","action":"removed","reason":"model_mismatch","future":true}]`,
		strings.Replace(responseZeroRetentionUsage, `"ttl_12h_tokens":0`, `"ttl_12h_tokens":-1`, 1),
		strings.Replace(responseZeroRetentionUsage, `"ttl_12h_tokens":0`, `"ttl_12h_tokens":0.5`, 1),
		strings.Replace(responseZeroRetentionUsage, `"ttl_12h_tokens":0`, `"ttl_12h_tokens":9223372036854775808`, 1),
		strings.Replace(responseZeroRetentionUsage, `,"ttl_12h_tokens":0`, ``, 1),
		strings.Replace(responseZeroRetentionUsage, `"ttl_12h_tokens":0`, `"ttl_12h_tokens":0,"future":0`, 1),
		strings.Replace(responseRetentionUsage, `"ttl_12h_tokens":4000`, `"ttl_12h_tokens":3000`, 1),
		strings.Replace(responseRetentionUsage, `"cache_write_tokens":7000`, `"cache_write_tokens":7000,"cached_creation_tokens":6000`, 1),
	} {
		_, err := compiled.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fields)), fixtureOptions(p.EvaluationContext{}))
		requireDraftRejection(t, err, "")
		frames := responsesPaddedTextFrames()
		for _, position := range []int{0, len(frames) - 1} {
			validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range frames[:position] {
				if err := validation.Consume(t.Context(), testValue(t, body)); err != nil {
					t.Fatal(err)
				}
			}
			err = validation.Consume(t.Context(), testValue(t, replaceResponseFrameFields(t, frames[position], fields)))
			validation.Release()
			requireDraftRejection(t, err, "/response")
		}
	}
}

func TestResponsesSavedLiveExtensions(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	raw := readResponseExtensionFixture(t, "json")
	response, err := from.DecodeResponse(t.Context(), raw, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	requireResponseMetadata(t, response.Metadata, "image", "false", "image-context-compression", "")
	requireResponseMetadata(t, response.Metadata, "input_transformations", "[]", "input-transformations", "")
	if response.Usage.Input.Count != 19 || response.Usage.Output.Count != 5 || response.Usage.Total.Count != 24 || response.Usage.CacheCreation.Count != 0 {
		t.Fatal("live zero duration detail changed aggregate accounting", response.Usage)
	}
	response.Native = nil
	clearMediaScopeNative(response.Content)
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionResponse, "compatible", "metadata")
		projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), nil)
		if err != nil {
			t.Fatal(target, err)
		}
		wire, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(target, err)
		}
		if target == Responses {
			sameJSON(t, wire, string(raw))
		}
	}
	var frames []string
	for _, line := range strings.Split(string(readResponseExtensionFixture(t, "sse")), "\n") {
		if strings.HasPrefix(line, "data:") {
			body := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if body != "[DONE]" {
				frames = append(frames, body)
			}
		}
	}
	if len(frames) != 9 {
		t.Fatal("saved full upstream stream lost frames", len(frames))
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		t.Run("stream/"+target, func(t *testing.T) {
			if output := auditConvertFrames(t, Responses, target, frames); len(output) == 0 {
				t.Fatal("saved stream did not produce target output")
			}
		})
	}
}
