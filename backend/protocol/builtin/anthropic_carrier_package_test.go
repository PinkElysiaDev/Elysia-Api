package builtin

import (
	"strings"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageCarrierHistory(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	const carrier = `{"type":"thinking","thinking":"","signature":"elysia-continuation.v1.synthetic"}`
	raw := `{"model":"m","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"text","text":"before"},` + carrier + `,{"type":"text","text":"after"}]}]}`
	input := testValue(t, raw)
	options, tokens, err := compiled.ExtractContinuations(t.Context(), input, p.EvaluationContext{})
	if err != nil || len(tokens) != 1 || tokens[0] != p.ContinuationPrefix+"synthetic" {
		t.Fatal("exact wrapper was not selected", tokens, err)
	}
	request, err := compiled.DecodeRequestValue(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Content) != 1 || len(request.Content[0].Children) != 2 {
		t.Fatal("carrier entered semantic history", request.Content)
	}
	for i, path := range []string{"/messages/0/content/0", "/messages/0/content/2"} {
		if source := request.Content[0].Children[i].Source; source == nil || source.Path != path {
			t.Fatal("carrier selection shifted original locations", i, source)
		}
	}
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err == nil {
		t.Fatal("unverified carrier was silently consumed")
	}
	if err := request.AcceptContinuations(tokens); err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		prepared := request.Clone()
		prepared.Model = p.StringValue("routed")
		if rebuild {
			prepared.Native = nil
		}
		wire, err := compiled.EncodeRequest(t.Context(), prepared, options)
		if err != nil || strings.Contains(string(wire), p.ContinuationPrefix) || !strings.Contains(string(wire), `"text":"before"`) || !strings.Contains(string(wire), `"text":"after"`) {
			t.Fatal("carrier replay leaked or changed ordinary history", rebuild, string(wire), err)
		}
	}
	for _, malformed := range []string{
		strings.Replace(carrier, `"thinking":""`, `"thinking":"visible"`, 1),
		strings.Replace(carrier, `"type":"thinking"`, `"type":"tool_use"`, 1),
		strings.TrimSuffix(carrier, "}") + `,"future":true}`,
	} {
		if _, _, err := compiled.ExtractContinuations(t.Context(), testValue(t, strings.Replace(raw, carrier, malformed, 1)), p.EvaluationContext{}); err == nil {
			t.Fatal("mixed wrapper was accepted", malformed)
		}
	}
}

func TestAnthropicPackageCarrierDelivery(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	source := p.Identity{Family: "gemini", WireVersion: "fixture", DefinitionID: "fixture"}
	codec, err := p.NewContinuationCodec([]byte("synthetic carrier key"))
	if err != nil {
		t.Fatal(err)
	}
	node := p.Node{Kind: p.TextNode, Payload: p.StringValue("hello"), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("provider-state"), Scope: scope}}}
	sealed, err := codec.SealState(p.ContinuationRecord{Version: 1, ID: "fixture", Protocol: source, Scope: scope, Node: node, Digest: p.ContinuationNodeDigest(node), ExpiresAt: time.Now().Add(time.Hour).Unix()}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
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
				input := testValue(t, `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)
				output, err := compiled.EncodeContinuationResponse(t.Context(), input, options)
				if err != nil {
					t.Fatal(err)
				}
				if err := compiled.ValidateContinuationResponse(t.Context(), output, options); err != nil {
					t.Fatal(err)
				}
				if strings.Count(output.JSONString(), sealed.Token()) != 1 || !binding.HasPending() {
					t.Fatal("carrier missing or delivery advanced before completion", output.JSONString())
				}
			} else {
				wire, err := compiled.NewWireStreamValidation(scope)
				if err != nil {
					t.Fatal(err)
				}
				options = options.WithOutputStream(wire)
				for _, raw := range []string{
					`{"type":"message_start","message":{"id":"r","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
					`{"type":"content_block_start","index":7,"content_block":{"type":"text","text":"hello"}}`,
					`{"type":"content_block_stop","index":7}`,
				} {
					frame := testValue(t, raw)
					if batch, err := compiled.EncodeContinuationFrame(t.Context(), frame, options); err != nil || batch != nil {
						t.Fatal("carrier emitted before the declared terminal boundary", batch, err)
					}
					if err := wire.ConsumeContinuation(t.Context(), frame, options); err != nil {
						t.Fatal(err)
					}
					if err := wire.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
				}
				batch, err := compiled.EncodeContinuationFrame(t.Context(), testValue(t, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`), options)
				if err != nil || len(batch) != 3 {
					t.Fatal("carrier requires one start, one stop and the original terminal", len(batch), err)
				}
				if !strings.Contains(batch[0].JSONString(), `"index":8`) || strings.Count(batch[0].JSONString(), sealed.Token()) != 1 {
					t.Fatal("carrier did not use the delivered sparse index", batch[0].JSONString())
				}
				for _, frame := range batch {
					if err := wire.ConsumeContinuation(t.Context(), frame, options); err != nil {
						t.Fatal(frame.JSONString(), err)
					}
				}
				if !binding.HasPending() {
					t.Fatal("validation was mistaken for delivery")
				}
				if err := wire.CommitDelivery(); err != nil {
					t.Fatal(err)
				}
				if err := binding.CommitDelivery(); err != nil {
					t.Fatal(err)
				}
				terminal := testValue(t, `{"type":"message_stop"}`)
				if repeated, err := compiled.EncodeContinuationFrame(t.Context(), terminal, options); err != nil || repeated != nil {
					t.Fatal("carrier repeated after delivery", repeated, err)
				}
				if err := wire.ConsumeContinuation(t.Context(), terminal, options); err != nil {
					t.Fatal(err)
				}
				if err := wire.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			if err := binding.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
			if err := binding.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
