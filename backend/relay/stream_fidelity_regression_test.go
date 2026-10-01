package relay

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestStreamUsageZeroAndMissingSnapshots(t *testing.T) {
	parse := func(body string) *MaheshvaraUsage { return maheshvaraUsageFromRawMap(cacheJSON(t, []byte(body))) }
	before := parse(`{"input_tokens":4,"output_tokens":8,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}`)
	merged := mergeMaheshvaraStreamUsage(before, parse(`{"output_tokens":0,"cache_read_input_tokens":0}`))
	if merged.InputTokens != 14 || merged.OutputTokens != 0 || merged.CachedInputTokens != 0 || merged.CacheCreationInputTokens != 10 || merged.TotalTokens != 14 {
		t.Fatalf("zero tail lost: %+v", merged)
	}
	if before.InputTokens != 34 || before.CachedInputTokens != 20 {
		t.Fatal("earlier snapshot was mutated")
	}
	observed := mergeMaheshvaraStreamUsage(parse(`{"input_tokens":8,"total_tokens":0}`), parse(`{"output_tokens":1}`))
	if observed.TotalTokens != 0 || observed.TotalTokensInferred {
		t.Fatalf("observed zero total overwritten: %+v", observed)
	}
	gemini := mergeMaheshvaraStreamUsage(parse(`{"promptTokenCount":8,"candidatesTokenCount":2}`), parse(`{"toolUsePromptTokenCount":3,"thoughtsTokenCount":4}`))
	if gemini.InputTokens != 11 || gemini.OutputTokens != 6 || gemini.TotalTokens != 17 {
		t.Fatalf("Gemini split components: %+v", gemini)
	}
	tiers := mergeMaheshvaraStreamUsage(parse(`{"input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":20}}`), parse(`{"cache_creation":{"ephemeral_1h_input_tokens":30}}`))
	if tiers.CacheCreationInputTokens != 50 || tiers.InputTokens != 60 {
		t.Fatalf("split TTL creation counts: %+v", tiers)
	}
}

func TestStreamIndependentUsageTailAllWireTargets(t *testing.T) {
	sources := map[FormatType][]string{
		FormatOpenAIChat: {
			`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":80,"completion_tokens":2,"total_tokens":82,"prompt_tokens_details":{"cached_tokens":30}}}`,
		},
		FormatClaude: {
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_delta","usage":{"input_tokens":50,"output_tokens":2,"cache_read_input_tokens":30}}`,
		},
		FormatGemini: {
			`{"candidates":[{"index":0,"content":{"parts":[{"text":"ok"}]}}]}`,
			`{"candidates":[{"index":0,"finishReason":"STOP"}]}`,
			`{"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":2,"totalTokenCount":82,"cachedContentTokenCount":30}}`,
		},
		FormatResponses: {
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ok"}`,
			`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
			`{"type":"response.usage.delta","usage":{"input_tokens":80,"output_tokens":2,"total_tokens":82,"input_tokens_details":{"cached_tokens":30}}}`,
		},
	}
	for source, frames := range sources {
		for _, target := range []FormatType{FormatOpenAIChat, FormatClaude, FormatGemini, FormatResponses} {
			t.Run(fmt.Sprintf("%s/%s", source, target), func(t *testing.T) {
				var input strings.Builder
				for _, frame := range frames {
					input.WriteString("data: " + frame + "\n\n")
				}
				writer := &captureStreamWriter{}
				if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(input.String()), source, target, writer, "test"); err != nil {
					t.Fatal(err)
				}
				decoder := NewMaheshvaraStreamDecoder(target)
				reader := NewSSEEventReader(strings.NewReader(writer.String()))
				defer reader.Close()
				var usage *MaheshvaraUsage
				for {
					frame, ok, err := reader.Read(context.Background(), DefaultSSEIdleTimeout)
					if err != nil {
						t.Fatal(err)
					}
					if !ok {
						break
					}
					events, err := decoder.Decode(frame)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						usage = mergeMaheshvaraStreamUsage(usage, event.Usage)
					}
				}
				if usage == nil || usage.InputTokens != 80 || usage.OutputTokens != 2 || usage.CachedInputTokens != 30 {
					t.Fatalf("tail lost: usage=%+v\n%s", usage, writer.String())
				}
			})
		}
	}
}

func TestStreamCumulativeRewriteRejected(t *testing.T) {
	decoder := NewMaheshvaraStreamDecoder(FormatOpenAIChat)
	if _, err := decoder.Decode(SSEEvent{Data: `{"choices":[{"index":0,"delta":{"content":"prefix"}}]}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Decode(SSEEvent{Data: `{"choices":[{"index":0,"message":{"content":"REWRITE"},"finish_reason":"stop"}]}`}); err == nil {
		t.Fatal("cumulative rewrite accepted")
	}
}

func TestStreamMalformedFunctionCompletionRejected(t *testing.T) {
	for _, arguments := range []string{"", `not JSON`, `{"q":`} {
		decoder := NewMaheshvaraStreamDecoder(FormatResponses)
		wire := fmt.Sprintf(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":%q}`, arguments)
		if _, err := decoder.Decode(SSEEvent{Data: wire}); err == nil {
			t.Fatalf("accepted malformed arguments: %q", arguments)
		}
	}
}

func TestStreamDeclaredSequenceDeduplication(t *testing.T) {
	decoder := NewMaheshvaraStreamDecoder(FormatResponses)
	frame := SSEEvent{Data: `{"type":"response.output_text.delta","sequence_number":0,"delta":"one"}`}
	if events, err := decoder.Decode(frame); err != nil || len(events) != 1 {
		t.Fatalf("first: %v %v", events, err)
	}
	if events, err := decoder.Decode(frame); err != nil || len(events) != 0 {
		t.Fatalf("duplicate: %v %v", events, err)
	}
	if _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.output_text.delta","sequence_number":0,"delta":"other"}`}); err == nil {
		t.Fatal("conflicting sequence accepted")
	}
}

func TestStreamCustomCumulativeRewriteRejected(t *testing.T) {
	config := CustomProtocolConfig{ID: "cumulative-arbitrary", Type: "llm", Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"model":"{{maheshvara.model}}"}`}, Response: CustomProtocolResponse{TextPath: "text", Stream: &CustomProtocolStreamMapping{Mode: "cumulative"}}}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.Decode(SSEEvent{Data: `{"text":"prefix"}`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.Decode(SSEEvent{Data: `{"text":"PREFIX"}`}); err == nil {
		t.Fatal("custom cumulative rewrite accepted")
	}
}

func TestSSEMultilineFrameLimit(t *testing.T) {
	line := "data: " + strings.Repeat("x", 1024) + "\n"
	reader := NewSSEEventReader(strings.NewReader(strings.Repeat(line, streamLimits.BufferBytes/1024+1)))
	defer reader.Close()
	if _, _, err := reader.Read(context.Background(), DefaultSSEIdleTimeout); err == nil {
		t.Fatal("unbounded multiline frame accepted")
	}
}
