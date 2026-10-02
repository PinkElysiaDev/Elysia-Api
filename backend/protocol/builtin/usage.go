package builtin

import (
	"fmt"
	"math"
	"slices"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func counter(value p.Value) (*p.Counter, error) {
	if value.IsZero() {
		return nil, nil
	}
	var count int64
	if value.IsNull() {
		return nil, fmt.Errorf("usage counter cannot be null")
	}
	if err := value.Decode(&count); err != nil || count < 0 {
		return nil, fmt.Errorf("usage counter must be a nonnegative int64")
	}
	return &p.Counter{Count: count, Origin: p.ObservedCount}, nil
}

func sumCounters(counts ...*p.Counter) (*p.Counter, error) {
	var total int64
	for _, count := range counts {
		if count == nil {
			return nil, nil
		}
		if count.Count > math.MaxInt64-total {
			return nil, fmt.Errorf("usage count overflow")
		}
		total += count.Count
	}
	return &p.Counter{Count: total, Origin: p.InferredCount}, nil
}

func nestedObject(fields p.Object, key string) (p.Object, error) {
	if fields[key].IsZero() {
		return p.Object{}, nil
	}
	return fields[key].ReadObject()
}

func (adapter module) decodeUsage(value p.Value) (*p.Usage, error) {
	if value.IsZero() || value.IsNull() {
		return nil, nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return nil, err
	}
	usage := &p.Usage{Details: map[string]p.Counter{}}
	input, output, total, read, creation := "input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"
	if adapter.name == Chat {
		input, output = "prompt_tokens", "completion_tokens"
	}
	if adapter.name == Gemini {
		input, output, total, read = "promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount"
	}
	for _, entry := range []struct {
		key    string
		target **p.Counter
	}{{input, &usage.Input}, {output, &usage.Output}, {total, &usage.Total}, {read, &usage.CacheRead}, {creation, &usage.CacheCreation}} {
		*entry.target, err = counter(fields[entry.key])
		if err != nil {
			return nil, err
		}
	}
	if adapter.name == Chat || adapter.name == Responses {
		inputDetails, outputDetails := "prompt_tokens_details", "completion_tokens_details"
		if adapter.name == Responses {
			inputDetails, outputDetails = "input_tokens_details", "output_tokens_details"
		}
		for _, entry := range []struct{ key, prefix string }{{inputDetails, "input."}, {outputDetails, "output."}} {
			details, err := nestedObject(fields, entry.key)
			if err != nil {
				return nil, err
			}
			for key, value := range details {
				allowed := inputUsageDetails
				if entry.prefix == "output." {
					allowed = outputUsageDetails
				}
				if !slices.Contains(allowed, key) {
					continue
				}
				count, err := counter(value)
				if err != nil {
					return nil, err
				}
				if key == "cached_tokens" && entry.prefix == "input." {
					usage.CacheRead = count
				} else if count != nil {
					usage.Details[entry.prefix+key] = *count
				}
			}
		}
	}
	if adapter.name == Anthropic {
		creationDetails, err := nestedObject(fields, "cache_creation")
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"} {
			count, err := counter(creationDetails[key])
			if err != nil {
				return nil, err
			}
			if count != nil {
				usage.Details[key] = *count
			}
		}
		if usage.CacheCreation == nil && len(creationDetails) > 0 {
			five, hasFive := usage.Details["ephemeral_5m_input_tokens"]
			one, hasOne := usage.Details["ephemeral_1h_input_tokens"]
			if hasFive && hasOne {
				usage.CacheCreation, err = sumCounters(&five, &one)
			}
			if err != nil {
				return nil, err
			}
		}
		if usage.Input != nil {
			usage.Details["uncached_input_tokens"] = *usage.Input
			for _, count := range []*p.Counter{usage.CacheRead, usage.CacheCreation} {
				if count != nil {
					sum, err := sumCounters(usage.Input, count)
					if err != nil {
						return nil, err
					}
					sum.Origin = p.ObservedCount
					usage.Input = sum
				}
			}
		}
	}
	if adapter.name == Gemini {
		for key, semantic := range map[string]string{"thoughtsTokenCount": "output.reasoning_tokens", "toolUsePromptTokenCount": "toolUsePromptTokenCount"} {
			count, err := counter(fields[key])
			if err != nil {
				return nil, err
			}
			if count != nil {
				usage.Details[semantic] = *count
			}
		}
		if thoughts, exists := usage.Details["output.reasoning_tokens"]; exists && usage.Output != nil {
			usage.Output, err = sumCounters(usage.Output, &thoughts)
			if err != nil {
				return nil, err
			}
			// Both wire components are observed. Normalizing their scope must
			// still replace an earlier snapshot containing only visible output.
			usage.Output.Origin = p.ObservedCount
		}
	}
	if usage.Total == nil {
		usage.Total, err = sumCounters(usage.Input, usage.Output)
		if err != nil {
			return nil, err
		}
	}
	return usage, nil
}

func counterValue(count *p.Counter) p.Value {
	if count == nil {
		return p.Value{}
	}
	value, _ := p.EncodeValue(count.Count)
	return value
}

func (adapter module) encodeUsage(usage *p.Usage) (p.Value, error) {
	if usage == nil {
		return p.Value{}, nil
	}
	if err := adapter.checkUsageDetails(usage); err != nil {
		return p.Value{}, err
	}
	fields := p.Object{}
	input, output, total := "input_tokens", "output_tokens", "total_tokens"
	if adapter.name == Chat {
		input, output = "prompt_tokens", "completion_tokens"
	}
	if adapter.name == Gemini {
		input, output, total = "promptTokenCount", "candidatesTokenCount", "totalTokenCount"
	}
	fields[input], fields[output], fields[total] = counterValue(usage.Input), counterValue(usage.Output), counterValue(usage.Total)
	switch adapter.name {
	case Anthropic:
		delete(fields, total)
		if usage.Input != nil {
			uncached := *usage.Input
			for _, count := range []*p.Counter{usage.CacheRead, usage.CacheCreation} {
				if count != nil {
					uncached.Count -= count.Count
				}
			}
			if uncached.Count < 0 {
				return p.Value{}, fmt.Errorf("cached input exceeds total input")
			}
			fields[input] = counterValue(&uncached)
		}
		fields["cache_read_input_tokens"], fields["cache_creation_input_tokens"] = counterValue(usage.CacheRead), counterValue(usage.CacheCreation)
		details := p.Object{}
		for _, key := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"} {
			if count, exists := usage.Details[key]; exists {
				details[key] = counterValue(&count)
			}
		}
		if len(details) > 0 {
			fields["cache_creation"] = object(details)
		}
	case Chat, Responses:
		in, out := p.Object{"cached_tokens": counterValue(usage.CacheRead)}, p.Object{}
		for key, count := range usage.Details {
			if len(key) > 6 && key[:6] == "input." {
				in[key[6:]] = counterValue(&count)
			}
			if len(key) > 7 && key[:7] == "output." {
				out[key[7:]] = counterValue(&count)
			}
		}
		inputDetails, outputDetails := "prompt_tokens_details", "completion_tokens_details"
		if adapter.name == Responses {
			inputDetails, outputDetails = "input_tokens_details", "output_tokens_details"
		}
		if usage.CacheRead != nil || len(in) > 1 {
			fields[inputDetails] = object(in)
		}
		if len(out) > 0 {
			fields[outputDetails] = object(out)
		}
		if usage.CacheCreation != nil {
			fields["cache_creation_input_tokens"] = counterValue(usage.CacheCreation)
		}
	case Gemini:
		fields["cachedContentTokenCount"] = counterValue(usage.CacheRead)
		if usage.CacheCreation != nil {
			return p.Value{}, unsupported("/usage/cacheCreation", "Gemini usage has no cache creation counter")
		}
		if count, exists := usage.Details["toolUsePromptTokenCount"]; exists {
			fields["toolUsePromptTokenCount"] = counterValue(&count)
		}
		if thoughts, exists := usage.Details["output.reasoning_tokens"]; exists {
			fields["thoughtsTokenCount"] = counterValue(&thoughts)
			if usage.Output != nil {
				visible := *usage.Output
				visible.Count -= thoughts.Count
				if visible.Count < 0 {
					return p.Value{}, fmt.Errorf("reasoning output exceeds total output")
				}
				fields[output] = counterValue(&visible)
			}
		}
	}
	return object(fields), nil
}

// The uncached subtotal is derivable from normalized input. Every other detail
// needs an explicit target representation, even when its count is zero.
func (adapter module) checkUsageDetails(usage *p.Usage) error {
	for name, count := range usage.Details {
		if name == "uncached_input_tokens" {
			if usage.Input == nil {
				return unsupported("/usage/details/"+name, "uncached subtotal requires total input")
			}
			expected := usage.Input.Count
			for _, cached := range []*p.Counter{usage.CacheRead, usage.CacheCreation} {
				if cached != nil {
					expected -= cached.Count
				}
			}
			if expected != count.Count {
				return fmt.Errorf("uncached subtotal disagrees with normalized input")
			}
			continue
		}
		isSupported := false
		switch adapter.name {
		case Chat, Responses:
			isSupported = strings.HasPrefix(name, "input.") || strings.HasPrefix(name, "output.")
		case Anthropic:
			isSupported = name == "ephemeral_5m_input_tokens" || name == "ephemeral_1h_input_tokens"
		case Gemini:
			isSupported = name == "output.reasoning_tokens" || name == "toolUsePromptTokenCount"
		}
		if !isSupported {
			return unsupported("/usage/details/"+name, "target has no equivalent usage detail")
		}
	}
	return nil
}
