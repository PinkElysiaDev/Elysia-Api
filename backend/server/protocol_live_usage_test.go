package server

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// This reference reads the wire counters independently of the gateway codec.
// Provider extensions remain evidence; an unknown alias is not assigned a
// meaning solely because its spelling contains the word cache.
func referenceLiveUsage(id string, frames []map[string]any) (*protocol.Usage, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	fields := mergeLiveUsageFrames(frames)
	input, output, total, read, creation := "input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"
	if id == "openai-chat-completions" {
		input, output = "prompt_tokens", "completion_tokens"
	}
	if id == "google-generate-content" {
		input, output, total, read = "promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount"
	}
	current := &protocol.Usage{}
	for _, field := range []struct {
		name    string
		counter **protocol.Counter
	}{{input, &current.Input}, {output, &current.Output}, {total, &current.Total}, {read, &current.CacheRead}, {creation, &current.CacheCreation}} {
		count, err := referenceLiveCounter(fields, field.name)
		if err != nil {
			return nil, err
		}
		*field.counter = count
	}
	if id == "openai-chat-completions" || id == "openai-responses" {
		key := "prompt_tokens_details"
		if id == "openai-responses" {
			key = "input_tokens_details"
		}
		if details, ok := fields[key].(map[string]any); ok {
			count, err := referenceLiveCounter(details, "cached_tokens")
			if err != nil {
				return nil, err
			}
			if count != nil {
				current.CacheRead = count
			}
			write, err := referenceLiveCounter(details, "cache_write_tokens")
			if err != nil {
				return nil, err
			}
			if write != nil {
				if current.CacheCreation != nil && current.CacheCreation.Count != write.Count {
					return nil, fmt.Errorf("raw creation counters disagree")
				}
				current.CacheCreation = write
			}
		}
	}
	if id == "anthropic-messages" && current.CacheCreation == nil {
		if details, ok := fields["cache_creation"].(map[string]any); ok {
			five, err := referenceLiveCounter(details, "ephemeral_5m_input_tokens")
			if err != nil {
				return nil, err
			}
			one, err := referenceLiveCounter(details, "ephemeral_1h_input_tokens")
			if err != nil {
				return nil, err
			}
			if five != nil && one != nil {
				if five.Count > math.MaxInt64-one.Count {
					return nil, fmt.Errorf("raw creation counter overflow")
				}
				current.CacheCreation = &protocol.Counter{Count: five.Count + one.Count, Origin: protocol.InferredCount}
			}
		}
	}
	if id == "anthropic-messages" && current.Input != nil {
		for _, extra := range []*protocol.Counter{current.CacheRead, current.CacheCreation} {
			if extra != nil {
				if current.Input.Count > math.MaxInt64-extra.Count {
					return nil, fmt.Errorf("raw input counter overflow")
				}
				current.Input.Count += extra.Count
			}
		}
	}
	if id == "google-generate-content" && current.Output != nil {
		thoughts, err := referenceLiveCounter(fields, "thoughtsTokenCount")
		if err != nil {
			return nil, err
		}
		if thoughts != nil {
			if current.Output.Count > math.MaxInt64-thoughts.Count {
				return nil, fmt.Errorf("raw output counter overflow")
			}
			current.Output.Count += thoughts.Count
		}
	}
	return current, nil
}

func TestLiveReferenceNormalizesLateComponentsOnce(t *testing.T) {
	frames := []map[string]any{{"input_tokens": json.Number("5"), "output_tokens": json.Number("2")}, {"cache_read_input_tokens": json.Number("15")}, {"cache_creation": map[string]any{"ephemeral_5m_input_tokens": json.Number("3")}}, {"cache_creation": map[string]any{"ephemeral_1h_input_tokens": json.Number("4")}}}
	u, err := referenceLiveUsage("anthropic-messages", frames)
	if err != nil || u.Input.Count != 27 || u.CacheCreation.Count != 7 {
		t.Fatal("late components lost", u, err)
	}
	frames = append(frames, map[string]any{"cache_read_input_tokens": json.Number("0")})
	u, err = referenceLiveUsage("anthropic-messages", frames)
	if err != nil || u.Input.Count != 12 || u.CacheRead.Count != 0 {
		t.Fatal("zero update lost", u, err)
	}
	if frames[0]["input_tokens"] != json.Number("5") {
		t.Fatal("reference mutated evidence")
	}
}

func referenceLiveCounter(fields map[string]any, name string) (*protocol.Counter, error) {
	value, exists := fields[name]
	if !exists {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("raw %s is not an integer", name)
	}
	count, err := number.Int64()
	if err != nil || count < 0 {
		return nil, fmt.Errorf("raw %s is not a nonnegative int64", name)
	}
	return &protocol.Counter{Count: count, Origin: protocol.ObservedCount}, nil
}

func TestLiveUsageReferenceDistinguishesTailsMissingAndZero(t *testing.T) {
	for _, fixture := range []struct {
		id, wire            string
		input, output, read int64
		hasRead             bool
	}{
		{"openai-chat-completions", `{"usage":{"prompt_tokens":100,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}}}`, 100, 2, 0, true},
		{"openai-responses", `{"usage":{"input_tokens":100,"output_tokens":2}}`, 100, 2, 0, false},
		{"anthropic-messages", `{"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}`, 100, 2, 80, true},
		{"google-generate-content", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2,"thoughtsTokenCount":3,"cachedContentTokenCount":80}}`, 100, 5, 80, true},
	} {
		usage, err := referenceLiveUsage(fixture.id, readLiveUsage([]byte(fixture.wire), protocol.Operation{}, false))
		if err != nil {
			t.Fatal(err)
		}
		if usage.Input.Count != fixture.input || usage.Output.Count != fixture.output || (usage.CacheRead != nil) != fixture.hasRead {
			t.Fatalf("%s: %+v", fixture.id, usage)
		}
		if fixture.hasRead && usage.CacheRead.Count != fixture.read {
			t.Fatal("cache read changed")
		}
	}
	usage, err := referenceLiveUsage("anthropic-messages", []map[string]any{{"input_tokens": json.Number("18"), "output_tokens": json.Number("0")}, {"input_tokens": json.Number("322"), "output_tokens": json.Number("5")}})
	if err != nil || usage.Input.Count != 322 || usage.Output.Count != 5 {
		t.Fatal("tail did not replace initial usage", err)
	}
}
