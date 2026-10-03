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
	var result *protocol.Usage
	for _, fields := range frames {
		if result == nil {
			result = &protocol.Usage{}
		}
		input, output, total, read, creation := "input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"
		if id == "chat-completions-api" {
			input, output = "prompt_tokens", "completion_tokens"
		}
		if id == "gemini-api" {
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
		if id == "chat-completions-api" || id == "responses-api" {
			key := "prompt_tokens_details"
			if id == "responses-api" {
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
			}
		}
		if id == "anthropic-api" && current.Input != nil {
			for _, extra := range []*protocol.Counter{current.CacheRead, current.CacheCreation} {
				if extra != nil {
					if current.Input.Count > math.MaxInt64-extra.Count {
						return nil, fmt.Errorf("raw input counter overflow")
					}
					current.Input.Count += extra.Count
				}
			}
		}
		if id == "gemini-api" && current.Output != nil {
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
		for _, pair := range []struct {
			source *protocol.Counter
			target **protocol.Counter
		}{{current.Input, &result.Input}, {current.Output, &result.Output}, {current.Total, &result.Total}, {current.CacheRead, &result.CacheRead}, {current.CacheCreation, &result.CacheCreation}} {
			if pair.source != nil {
				*pair.target = pair.source
			}
		}
	}
	return result, nil
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
		{"chat-completions-api", `{"usage":{"prompt_tokens":100,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}}}`, 100, 2, 0, true},
		{"responses-api", `{"usage":{"input_tokens":100,"output_tokens":2}}`, 100, 2, 0, false},
		{"anthropic-api", `{"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}`, 100, 2, 80, true},
		{"gemini-api", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2,"thoughtsTokenCount":3,"cachedContentTokenCount":80}}`, 100, 5, 80, true},
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
	usage, err := referenceLiveUsage("anthropic-api", []map[string]any{{"input_tokens": json.Number("18"), "output_tokens": json.Number("0")}, {"input_tokens": json.Number("322"), "output_tokens": json.Number("5")}})
	if err != nil || usage.Input.Count != 322 || usage.Output.Count != 5 {
		t.Fatal("tail did not replace initial usage", err)
	}
}
