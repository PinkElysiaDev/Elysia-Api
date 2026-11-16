package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestStreamUsageLateComponentsRecomputeTotals(t *testing.T) {
	for _, fixture := range []struct {
		name                          string
		updates                       []string
		input, output, read, creation int64
	}{
		{Anthropic, []string{`{"input_tokens":5,"output_tokens":2}`, `{"cache_read_input_tokens":15}`, `{"cache_creation":{"ephemeral_5m_input_tokens":3}}`, `{"cache_creation":{"ephemeral_1h_input_tokens":4}}`, `{"cache_read_input_tokens":0}`}, 12, 2, 0, 7},
		{Gemini, []string{`{"promptTokenCount":20,"candidatesTokenCount":2}`, `{"thoughtsTokenCount":7}`, `{"cachedContentTokenCount":15}`, `{"candidatesTokenCount":4}`, `{"thoughtsTokenCount":0}`}, 20, 4, 15, 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snapshots := decodeUsageSnapshots(t, fixture.name, fixture.updates)
			for index, usage := range snapshots {
				if fixture.name == Anthropic && index == 2 && usage.CacheCreation != nil {
					t.Fatal("missing TTL bucket treated as zero")
				}
				if fixture.name == Gemini && index == 1 && usage.Output.Count != 9 {
					t.Fatal("late thoughts lost", usage.Output)
				}
			}
			usage := snapshots[len(snapshots)-1]
			if usage.Input.Count != fixture.input || usage.Output.Count != fixture.output || usage.Total.Count != fixture.input+fixture.output || usage.CacheRead.Count != fixture.read {
				t.Fatalf("late component normalization: %+v", usage)
			}
			if fixture.creation > 0 && (usage.CacheCreation == nil || usage.CacheCreation.Count != fixture.creation) {
				t.Fatal(usage.CacheCreation)
			}
			if _, err := encodeUsageResponse(t, testCompiled(t, fixture.name), usage); err != nil {
				t.Fatal("normalized counters cannot be rendered", err)
			}
		})
	}
}

func TestAnthropicPartialUsageWireContract(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	const start = `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{}}}`
	for _, field := range []string{"input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation/ephemeral_5m_input_tokens", "cache_creation/ephemeral_1h_input_tokens", "output_tokens_details/thinking_tokens"} {
		for _, invalid := range []string{`null`, `-1`, `"3"`, `1.5`, `9223372036854775808`} {
			t.Run(field+"/"+invalid, func(t *testing.T) {
				validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
				if err != nil {
					t.Fatal(err)
				}
				defer validation.Release()
				if err := validation.Consume(t.Context(), testValue(t, start)); err != nil {
					t.Fatal(err)
				}
				name, child, hasChild := strings.Cut(field, "/")
				value := invalid
				if hasChild {
					value = fmt.Sprintf(`{"%s":%s}`, child, value)
				}
				frame := fmt.Sprintf(`{"type":"message_delta","delta":{},"usage":{"%s":%s}}`, name, value)
				if err := validation.Consume(t.Context(), testValue(t, frame)); err == nil {
					t.Fatal("source Spec accepted invalid partial counter", frame)
				}
			})
		}
	}
}
