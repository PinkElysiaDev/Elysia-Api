package builtin

import (
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
			stream := &streamModule{module: module{name: fixture.name}, limits: p.DefaultLimits()}
			var usage *p.Usage
			for index, raw := range fixture.updates {
				update, err := stream.decodeUsageUpdate(testValue(t, raw))
				if err != nil {
					t.Fatal(err)
				}
				usage = p.MergeUsage(usage, update)
				if fixture.name == Anthropic && index == 2 && usage.CacheCreation != nil {
					t.Fatal("missing TTL bucket treated as zero")
				}
				if fixture.name == Gemini && index == 1 && usage.Output.Count != 9 {
					t.Fatal("late thoughts lost", usage.Output)
				}
			}
			if usage.Input.Count != fixture.input || usage.Output.Count != fixture.output || usage.Total.Count != fixture.input+fixture.output || usage.CacheRead.Count != fixture.read {
				t.Fatalf("late component normalization: %+v", usage)
			}
			if fixture.creation > 0 && (usage.CacheCreation == nil || usage.CacheCreation.Count != fixture.creation) {
				t.Fatal(usage.CacheCreation)
			}
			if _, err := stream.module.encodeUsage(usage, p.EvaluationContext{}); err != nil {
				t.Fatal("normalized counters cannot be rendered", err)
			}
		})
	}
}
