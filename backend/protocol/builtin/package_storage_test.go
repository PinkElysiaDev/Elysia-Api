package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func packageStorageWire(name, raw string) string {
	field := ""
	if raw != "" {
		field = `,"store":` + raw
	}
	if name == "openai-chat" {
		return `{"model":"m","messages":[{"role":"user","content":"hello"}]` + field + `}`
	}
	return `{"model":"m","input":"hello"` + field + `}`
}

func TestPackageOpenAIStorageOwner(t *testing.T) {
	for _, fixture := range []struct {
		name, codec string
		defaultOn   bool
	}{{"openai-chat", "openai-chat", false}, {"openai-responses", "responses", true}} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.name)
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, compiled, compiled, "responses-storage", "strict"))
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
			for _, raw := range []string{"", "false", "true", "null"} {
				t.Run("store="+raw, func(t *testing.T) {
					original := packageStorageWire(fixture.name, raw)
					request, err := compiled.DecodeRequest(t.Context(), []byte(original), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					want := p.ResponsesStorageIntent{SourceCodec: fixture.codec, Requested: raw == "true" || raw == "" && fixture.defaultOn, RequestedUnknown: raw == "null", EffectiveUnknown: true}
					if raw != "" {
						want.Raw = testValue(t, raw)
					}
					if request.ClientOutput == nil || request.ClientOutput.ResponsesStorage == nil || *request.ClientOutput.ResponsesStorage != want || !request.Parameters["store"].IsZero() {
						t.Fatal("source storage facts were lost or duplicated", request.ClientOutput, request.Parameters)
					}
					direct, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
					if err != nil || string(direct) != original {
						t.Fatal("direct same-contract encoding changed source bytes", string(direct), err)
					}
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, route, sink)
					if err != nil || len(sink.Issues()) != 0 || *request.ClientOutput.ResponsesStorage != want {
						t.Fatal("same-contract storage introduced loss or changed source facts", err, sink.Issues())
					}
					want.Effective, want.EffectiveUnknown = want.Requested, want.RequestedUnknown
					if *projected.ClientOutput.ResponsesStorage != want {
						t.Fatal("target storage decision differs from source contract", projected.ClientOutput)
					}
					for _, mode := range []string{"native", "model", "rebuild"} {
						prepared := projected.Clone()
						if mode == "model" {
							prepared.Model = p.StringValue("other-model")
						}
						if mode == "rebuild" {
							prepared.Native = nil
						}
						options := p.EvaluationContext{ClientOutput: prepared.ClientOutput}
						wire, err := compiled.EncodeRequest(t.Context(), prepared, options)
						if err != nil {
							t.Fatal(mode, err)
						}
						value := testValue(t, string(wire))
						if err := compiled.ValidateRequestOutput(t.Context(), value, options); err != nil {
							t.Fatal(mode, err)
						}
						var fields p.Object
						if err := value.Decode(&fields); err != nil || fields["store"] != want.Raw || fields["model"] != prepared.Model {
							t.Fatal("storage representation or model changed", mode, string(wire), err)
						}
					}
					// A user replacement rule may change the effective instruction.
					// Raw remains source evidence and cannot override that decision.
					changed := projected.Clone()
					changed.ClientOutput.ResponsesStorage.Effective = !want.Requested
					changed.ClientOutput.ResponsesStorage.EffectiveUnknown = false
					options := p.EvaluationContext{ClientOutput: changed.ClientOutput}
					wire, err := compiled.EncodeRequest(t.Context(), changed, options)
					if err != nil {
						t.Fatal("changed effective instruction", err)
					}
					value := testValue(t, string(wire))
					if err := compiled.ValidateRequestOutput(t.Context(), value, options); err != nil {
						t.Fatal(err)
					}
					var fields p.Object
					if err := value.Decode(&fields); err != nil || fields["store"].JSONString() != fmt.Sprint(!want.Requested) {
						t.Fatal("raw storage defeated the target decision", string(wire), err)
					}
					if err := compiled.ValidateRequestOutput(t.Context(), testValue(t, packageStorageWire(fixture.name, fmt.Sprint(want.Requested))), options); err == nil {
						t.Fatal("a final wire edit bypassed effective storage")
					}
				})
			}
			for _, raw := range []string{`"false"`, `0`, `{}`, `[]`} {
				if _, err := compiled.DecodeRequest(t.Context(), []byte(packageStorageWire(fixture.name, raw)), p.EvaluationContext{}); err == nil {
					t.Fatal("invalid source store type was accepted", raw)
				}
			}
		})
	}
}

func TestPackageOpenAICrossStorageDisposition(t *testing.T) {
	chat, responses := compileBuiltinPackage(t, "openai-chat"), compileBuiltinPackage(t, "openai-responses")
	for _, pair := range []struct {
		name, sourceName string
		source, target   *p.Compiled
	}{{"chat-to-responses", "openai-chat", chat, responses}, {"responses-to-chat", "openai-responses", responses, chat}} {
		t.Run(pair.name, func(t *testing.T) {
			route := p.ConversionContext{Source: pair.source.Identity(), Target: pair.target.Identity()}
			for _, mode := range []string{"compatible", "strict"} {
				policy := declaredRulePolicy(t, pair.source, pair.target, "responses-storage", mode)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				for _, raw := range []string{"false", "true", "null"} {
					request, err := pair.source.DecodeRequest(t.Context(), []byte(packageStorageWire(pair.sourceName, raw)), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, route, sink)
					if mode == "strict" && raw != "false" {
						var failure *p.ConversionError
						if !errors.As(err, &failure) || failure.Issues[0].RuleID != "responses-storage" || failure.Issues[0].Code != p.ConversionRejected {
							t.Fatal("strict storage loss was not attributed", raw, err)
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					want := *request.ClientOutput.ResponsesStorage
					want.Effective, want.EffectiveUnknown = raw == "true", false
					if *projected.ClientOutput.ResponsesStorage != want {
						t.Fatal("target replaced storage source facts", projected.ClientOutput)
					}
					if issues := sink.Issues(); raw == "false" && len(issues) != 0 || raw != "false" && (len(issues) != 1 || issues[0].PolicyHash != conversion.Hash || issues[0].Code != p.ConversionDegraded) {
						t.Fatal("wrong storage loss diagnostics", raw, issues)
					}
					options := p.EvaluationContext{ClientOutput: projected.ClientOutput}
					wire, err := pair.target.EncodeRequest(t.Context(), projected, options)
					if err != nil {
						t.Fatal(err)
					}
					if err := pair.target.ValidateRequestOutput(t.Context(), testValue(t, string(wire)), options); err != nil {
						t.Fatal(err)
					}
				}
				policy.Rules[0].Enabled = false
				disabled, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				request, err := pair.source.DecodeRequest(t.Context(), []byte(packageStorageWire(pair.sourceName, "false")), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				projected, err := disabled.Request(t.Context(), request, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pair.target.EncodeRequest(t.Context(), projected, p.EvaluationContext{}); err == nil {
					t.Fatal("encoder silently restored a disabled cross-contract rule")
				}
			}
		})
	}
}

func TestResponsesPackageStorageDelivery(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	const raw = `{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[{"type":"message","id":"msg","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}],"store":true,"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}`
	options := p.EvaluationContext{ClientOutput: &p.ClientOutput{ResponsesStorage: &p.ResponsesStorageIntent{Requested: true, Effective: false}}}
	response, err := compiled.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeResponse(t.Context(), response, options)
	if err != nil {
		t.Fatal(err)
	}
	var fields p.Object
	if err := testValue(t, string(wire)).Decode(&fields); err != nil || fields["store"] != testValue(t, `false`) {
		t.Fatal("JSON native reconciliation bypassed storage delivery", string(wire), err)
	}
	request := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Parameters: p.Object{"store": testValue(t, `false`)}}
	if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
		t.Fatal("legacy storage owner still encoded")
	}
	declaration, _ := compiled.Package()
	var input []p.Value
	for _, sample := range declaration.Mapping.Samples {
		if sample.ID == "declared-stream-text-decode" {
			if err := sample.Input.Decode(&input); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if len(input) == 0 {
		t.Fatal("missing stream fixture")
	}
	decode := p.EvaluationContext{State: p.NewEvaluationState()}
	encode := options
	encode.State = p.NewEvaluationState()
	validation, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	encode = encode.WithOutputStream(validation)
	encode, err = encode.WithOperation("storage-fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer decode.State.Release()
	defer encode.State.Release()
	envelopes, terminals := 0, 0
	consume := func(batch []p.Value) {
		t.Helper()
		for _, output := range batch {
			if err := validation.Consume(t.Context(), output); err != nil {
				t.Fatal("target contract", output.JSONString(), err)
			}
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
			var fields struct {
				Type     string   `json:"type"`
				Response p.Object `json:"response"`
			}
			if err := output.Decode(&fields); err != nil {
				t.Fatal(err)
			}
			if fields.Response != nil {
				envelopes++
				if fields.Response["store"] != testValue(t, `false`) {
					t.Fatal("native SSE replay bypassed storage delivery", output.JSONString())
				}
			}
			if fields.Type == "response.completed" {
				terminals++
				if !fields.Response["status"].IsString("completed") {
					t.Fatal("storage delivery changed terminal status", output.JSONString())
				}
				sameJSON(t, fields.Response["usage"].Bytes(), `{"input_tokens":20,"output_tokens":2,"total_tokens":22,"input_tokens_details":{"cached_tokens":15}}`)
			}
		}
	}
	for index, original := range input {
		var frameFields p.Object
		if err := original.Decode(&frameFields); err != nil {
			t.Fatal(err)
		}
		if envelope := frameFields["response"]; envelope.IsObject() {
			var responseFields p.Object
			if err := envelope.Decode(&responseFields); err != nil {
				t.Fatal(err)
			}
			responseFields["store"] = testValue(t, `true`)
			frameFields["response"], _ = p.EncodeValue(responseFields)
		}
		frameFields["sequence_number"] = testValue(t, fmt.Sprint(index))
		original, _ = p.EncodeValue(frameFields)
		frame, err := compiled.DecodeFrame(t.Context(), original, decode)
		if err != nil {
			t.Fatal(index, err)
		}
		batch, err := compiled.EncodeFrame(t.Context(), frame, encode)
		if err != nil {
			t.Fatal(index, err)
		}
		consume(batch)
	}
	tail, err := compiled.FinishEvents(t.Context(), encode)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
	if envelopes != 2 || terminals != 1 {
		t.Fatal("storage stream did not complete once", envelopes, terminals)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	failure, err := compiled.DecodeResponse(t.Context(), []byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`), p.EvaluationContext{Values: p.Object{"httpStatus": testValue(t, `400`)}})
	if err != nil {
		t.Fatal(err)
	}
	wire, err = compiled.EncodeResponse(t.Context(), failure, options)
	if err != nil || strings.Contains(string(wire), `"store"`) {
		t.Fatal("error envelope gained a storage instruction", string(wire), err)
	}
}
