package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Compilation and fixture decoding are excluded. Each iteration owns fresh
// conversion/delivery state; no result or mutable state crosses iterations.
func BenchmarkReferenceLayoutRewrite(b *testing.B) {
	source, target := compileBuiltinPackage(b, "openai-responses"), compileBuiltinPackage(b, "openai-chat")
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		b.Fatal(err)
	}
	const messages = 32
	text := "😀text" + strings.Repeat("x", 4096)
	annotation := testValue(b, `[{"type":"url_citation","url":"https://example.test","title":"reference","start_index":2,"end_index":6}]`)
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Attributes: p.Object{"created_at": testValue(b, "1")}}
	for range messages {
		response.Content = append(response.Content, p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(text), Metadata: []p.ResponseMetadata{{Name: "annotations", Codec: "responses", Location: "content", Value: annotation}}}}})
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	var wire []byte
	b.ReportAllocs()
	for b.Loop() {
		projected, err := conversion.Response(b.Context(), response, route, nil)
		if err != nil {
			b.Fatal(err)
		}
		wire, err = target.EncodeResponse(b.Context(), projected, p.EvaluationContext{})
		if err != nil {
			b.Fatal(err)
		}
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content     string
				Annotations []struct {
					URLCitation struct {
						Start int `json:"start_index"`
						End   int `json:"end_index"`
					} `json:"url_citation"`
				}
			}
		}
	}
	if err := json.Unmarshal(wire, &result); err != nil || len(result.Choices) != 1 {
		b.Fatal("invalid rewritten response", err)
	}
	message := result.Choices[0].Message
	if message.Content != strings.Repeat(text, messages) || len(message.Annotations) != messages {
		b.Fatal("rewrite lost content or references")
	}
	for index, annotation := range message.Annotations {
		start := index*(4096+6) + 2
		if annotation.URLCitation.Start != start || annotation.URLCitation.End != start+4 {
			b.Fatal("rewrite changed the Unicode reference interval", index, annotation)
		}
	}
}

func BenchmarkNativeThinkingContinuation(b *testing.B) {
	compiled := compileBuiltinPackage(b, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	frames, _ := decodePackageStream(b, compiled, []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":7,"content_block":{"type":"thinking","thinking":"prefix","signature":"sig-"}}`,
		`{"type":"content_block_delta","index":7,"delta":{"type":"thinking_delta","thinking":"original"}}`,
		`{"type":"content_block_delta","index":7,"delta":{"type":"thinking_delta","thinking":"native"}}`,
		`{"type":"content_block_delta","index":7,"delta":{"type":"signature_delta","signature":"native"}}`,
		`{"type":"content_block_stop","index":7}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}, scope)
	var rendered []string
	b.ReportAllocs()
	for b.Loop() {
		rendered = benchmarkNativeContinuation(b, compiled, frames, scope)
	}
	_, response := decodePackageStream(b, compiled, rendered, scope)
	if len(response.Content) != 1 || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("prefixeditednative") {
		b.Fatal("Native continuation repeated or lost the edited prefix")
	}
}

func benchmarkNativeContinuation(b *testing.B, compiled *p.Compiled, frames []*p.EventFrame, scope p.Scope) []string {
	wire, err := compiled.NewWireStreamValidation(scope)
	if err != nil {
		b.Fatal(err)
	}
	options := (p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}).WithOutputStream(wire)
	defer options.State.Release()
	var rendered []string
	consume := func(values []p.Value, err error) {
		if err != nil {
			b.Fatal(err)
		}
		for _, value := range values {
			if err := wire.Consume(b.Context(), value); err != nil {
				b.Fatal(err)
			}
			if err := wire.CommitDelivery(); err != nil {
				b.Fatal(err)
			}
			rendered = append(rendered, value.JSONString())
		}
	}
	for index, frame := range frames {
		events := frame.Events
		if index == 2 {
			events = append([]p.Event(nil), events...)
			for position := range events {
				events[position].Delta = p.StringValue("edited")
			}
		}
		consume(compiled.EncodeConvertedFrame(b.Context(), frame, events, options))
	}
	consume(compiled.FinishEvents(b.Context(), options))
	if err := wire.Finish(); err != nil {
		b.Fatal(err)
	}
	return rendered
}
