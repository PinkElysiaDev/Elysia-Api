package builtin

import (
	"strings"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageResponsesIncludeOwner(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	target := compileBuiltinPackage(t, "openai-chat")
	for _, raw := range []string{"", "null", "[]", `["reasoning.encrypted_content"]`, `["future.selector"]`} {
		t.Run(raw, func(t *testing.T) {
			field := ""
			if raw != "" {
				field = `,"include":` + raw
			}
			input := testValue(t, `{"model":"m","store":false,"input":"hello"`+field+`}`)
			request, err := source.DecodeRequestValue(t.Context(), input, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			output := request.ClientOutput
			if output.RawResponsesInclude.JSONString() != raw || output.RequireContinuationState != strings.Contains(raw, "reasoning.encrypted_content") || !request.Parameters["responses_include"].IsZero() {
				t.Fatal("include intent lost its unique owner", output, request.Parameters)
			}
			for _, native := range []bool{true, false} {
				copy := request.Clone()
				if !native {
					copy.Native = nil
				}
				options := p.EvaluationContext{ClientOutput: copy.ClientOutput}
				wire, err := source.EncodeRequestValue(t.Context(), copy, options)
				if err != nil {
					t.Fatal(native, err)
				}
				fields, err := wire.ReadObject()
				if err != nil || fields["include"].JSONString() != raw {
					t.Fatal("same-contract include changed", wire, err)
				}
				if err := source.ValidateRequestOutput(t.Context(), wire, options); err != nil {
					t.Fatal(err)
				}
			}
			for _, enabled := range []bool{true, false} {
				policy := p.DefaultConversionPolicy(source, target)
				for i := range policy.Rules {
					if policy.Rules[i].ID == "responses-include" {
						policy.Rules[i].Enabled = enabled
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				converted, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m"}, nil)
				if err == nil {
					_, err = target.EncodeRequestValue(t.Context(), converted, p.EvaluationContext{ClientOutput: converted.ClientOutput})
				}
				wantError := strings.Contains(raw, "future.selector") || !enabled && raw != ""
				if (err != nil) != wantError {
					t.Fatal("include disposition boundary changed", enabled, raw, err)
				}
			}
		})
	}
	for _, compiled := range []*p.Compiled{source, target} {
		conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, compiled, "responses-include", "compatible"))
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`"opaque"`, `[7]`} {
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{}, ClientOutput: &p.ClientOutput{RawResponsesInclude: testValue(t, raw)}}
			if _, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: compiled.Identity()}, nil); err == nil {
				t.Fatal("core input bypassed the declared include type", compiled.Identity(), raw)
			}
		}
	}
}

func TestPackageOpenAICarrierDelivery(t *testing.T) {
	scope := p.Scope{Provider: "synthetic", Account: "owner", Model: "m"}
	source := p.Identity{DefinitionID: "synthetic-source", Family: "gemini", WireVersion: "v2"}
	codec, err := p.NewContinuationCodec([]byte("synthetic test key"))
	if err != nil {
		t.Fatal(err)
	}
	node := p.Node{Kind: p.TextNode, Payload: p.StringValue("hello"), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("synthetic-signature"), Scope: scope}}}
	sealed, err := codec.SealState(p.ContinuationRecord{Version: 1, ID: "carrier", Protocol: source, Scope: scope, Node: node, Digest: p.ContinuationNodeDigest(node), ExpiresAt: time.Now().Add(time.Hour).Unix()}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			for _, direction := range []p.Direction{p.EncodeResponse, p.EncodeEvent} {
				t.Run(string(direction), func(t *testing.T) {
					binding, err := compiled.BindContinuation(direction, source, scope, true)
					if err != nil {
						t.Fatal(err)
					}
					if err := binding.Append(sealed); err != nil {
						t.Fatal(err)
					}
					options := (p.EvaluationContext{Scope: scope}).WithContinuation(binding)
					if direction == p.EncodeResponse {
						raw := `{"id":"r","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
						if name == "openai-responses" {
							raw = `{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[]}`
						}
						wire, err := compiled.EncodeContinuationResponse(t.Context(), testValue(t, raw), options)
						if err != nil {
							t.Fatal(err)
						}
						if err := compiled.ValidateContinuationResponse(t.Context(), wire, options); err != nil || strings.Count(wire.JSONString(), sealed.Token()) != 1 {
							t.Fatal("carrier missing from actual final response", wire, err)
						}
					} else {
						validation, err := compiled.NewWireStreamValidation(scope)
						if err != nil {
							t.Fatal(err)
						}
						options = options.WithOutputStream(validation)
						start := `{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`
						end := `{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
						if name == "openai-responses" {
							start = `{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","created_at":1,"model":"m","status":"in_progress","output":[]}}`
							end = `{"type":"response.completed","sequence_number":1,"response":{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[]}}`
						}
						frames, err := compiled.EncodeContinuationFrame(t.Context(), testValue(t, start), options)
						if err != nil || frames != nil {
							t.Fatal("nonterminal frame changed", frames, err)
						}
						if err := validation.ConsumeTransformed(t.Context(), testValue(t, start), testValue(t, strings.Replace(start, `"model":"m"`, `"model":"alias"`, 1)), options); err != nil {
							t.Fatal(err)
						}
						if err := validation.CommitDelivery(); err != nil {
							t.Fatal(err)
						}
						before := testValue(t, end)
						after := testValue(t, strings.Replace(end, `"model":"m"`, `"model":"alias"`, 1))
						originals, frames, err := compiled.EncodeContinuationTransformedFrames(t.Context(), before, after, options)
						if err != nil || len(frames) == 0 {
							t.Fatal(frames, err)
						}
						if len(originals) != len(frames) {
							t.Fatal("carrier frames lost their pre-rule pairing")
						}
						for index, frame := range frames {
							if strings.ReplaceAll(originals[index].JSONString(), `"model":"m"`, `"model":"alias"`) != frame.JSONString() {
								t.Fatal("carrier expansion changed the wire edit", originals[index], frame)
							}
							if err := validation.ConsumeTransformed(t.Context(), originals[index], frame, options); err != nil {
								t.Fatal(frame, err)
							}
						}
						if err := validation.Finish(); err != nil {
							t.Fatal(err)
						}
						if err := validation.CommitDelivery(); err != nil {
							t.Fatal(err)
						}
					}
					if !binding.HasPending() {
						t.Fatal("validation claimed delivery before the send boundary")
					}
					if err := binding.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
					if err := binding.Finish(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestPackageResponsesCarrierSourceLocations(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	const token = p.ContinuationPrefix + "synthetic"
	const wrapper = `{"type":"reasoning","id":"elysia_continuation_test","summary":[],"encrypted_content":"` + token + `"}`
	const following = `{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}`
	input := testValue(t, `{"model":"m","input":[`+wrapper+`,`+following+`]}`)
	options, tokens, err := compiled.ExtractContinuations(t.Context(), input, p.EvaluationContext{})
	if err != nil || len(tokens) != 1 || tokens[0] != token {
		t.Fatal(tokens, err)
	}
	request, err := compiled.DecodeRequestValue(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Content) != 1 || request.Content[0].Source == nil || request.Content[0].Source.Path != "/input/1" || request.Native == nil || request.Native.Value != input {
		t.Fatal("carrier removal shifted original source evidence", request.Content, request.Native)
	}
	if _, err := compiled.EncodeRequestValue(t.Context(), request, options); err == nil {
		t.Fatal("unaccepted carrier reached request encoding")
	}
	// Authentication itself belongs to the host; this checks the exact handoff.
	if err := request.AcceptContinuations(tokens); err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequestValue(t.Context(), request, options)
	if err != nil || strings.Contains(wire.JSONString(), p.ContinuationPrefix) || !strings.Contains(wire.JSONString(), "continue") {
		t.Fatal("native reconciliation reintroduced the consumed carrier", wire, err)
	}
	mixed := testValue(t, strings.Replace(input.JSONString(), `"summary":[]`, `"summary":[{"type":"summary_text","text":"must not disappear"}]`, 1))
	if _, _, err := compiled.ExtractContinuations(t.Context(), mixed, p.EvaluationContext{}); err == nil {
		t.Fatal("carrier wrapper was allowed to consume visible content")
	}
}
