package builtin

import (
	"fmt"
	"maps"
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
				} else if key == "cache_write_tokens" && entry.prefix == "input." {
					if usage.CacheCreation != nil && count != nil && usage.CacheCreation.Count != count.Count {
						return nil, fmt.Errorf("cache_write_tokens disagrees with cache_creation_input_tokens")
					}
					usage.CacheCreation = count
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

// Include recognized aliases in both sides of native reconciliation. Otherwise
// moving a canonical counter to its standard nested field leaves an old alias
// looking like an unknown extension, allowing it to survive edits or deletion.
func (adapter module) encodeResponseUsage(response *p.Response, options p.EvaluationContext) (p.Value, error) {
	encoded, err := adapter.encodeUsage(response.Usage, options)
	if err != nil || encoded.IsZero() || response.Native == nil || (adapter.name != Chat && adapter.name != Responses) {
		return encoded, err
	}
	if !p.CanPreserveNative(response.Native.Source, p.Target{Protocol: options.Identity(), Direction: p.EncodeResponse, Scope: options.Scope}) {
		return encoded, nil
	}
	root, err := response.Native.Value.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	original, err := nestedObject(root, "usage")
	if err != nil {
		return p.Value{}, err
	}
	if original["cache_read_input_tokens"].IsZero() && original["cache_creation_input_tokens"].IsZero() {
		return encoded, nil
	}
	fields, err := encoded.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	for _, alias := range []struct {
		name    string
		counter *p.Counter
	}{{"cache_read_input_tokens", response.Usage.CacheRead}, {"cache_creation_input_tokens", response.Usage.CacheCreation}} {
		if !original[alias.name].IsZero() {
			fields[alias.name] = counterValue(alias.counter)
		}
	}
	return object(fields), nil
}

func (adapter module) encodeUsage(usage *p.Usage, options p.EvaluationContext) (p.Value, error) {
	if usage == nil {
		return p.Value{}, nil
	}
	if err := adapter.checkUsageDetails(usage, options); err != nil {
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
		in, out := p.Object{"cached_tokens": counterValue(usage.CacheRead), "cache_write_tokens": counterValue(usage.CacheCreation)}, p.Object{}
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
		if usage.CacheRead != nil || usage.CacheCreation != nil || len(in) > 2 {
			fields[inputDetails] = object(in)
		}
		if len(out) > 0 {
			fields[outputDetails] = object(out)
		}
	case Gemini:
		fields["cachedContentTokenCount"] = counterValue(usage.CacheRead)
		// Gemini reports cache reads only. The provider's creation total has no
		// target field, so it is projected away and recorded rather than failing
		// a response the client can otherwise consume.
		if usage.CacheCreation != nil {
			options.Diagnostics.Add(p.ConversionIssue{
				Code: p.UnsupportedCapability, Severity: p.SeverityWarning, Protocol: options.Identity(),
				Direction: p.EncodeResponse, Stage: "wire", Path: "/usage/cacheCreation",
				Reason:     "cache creation total omitted: target has no cache creation counter",
				Suggestion: "Compare cache reads through cachedContentTokenCount; Gemini does not report cache writes.",
			})
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
//
// Details are traversed in sorted order so a rejected target reports the same
// path on every run; map iteration order is not stable across processes.
func (adapter module) checkUsageDetails(usage *p.Usage, options p.EvaluationContext) error {
	for _, name := range slices.Sorted(maps.Keys(usage.Details)) {
		count := usage.Details[name]
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
		// The canonical counters own these names. An expression that writes the
		// detail form would otherwise overwrite the count chosen above, leaving
		// no signal that the two disagreed.
		if adapter.name == Chat || adapter.name == Responses {
			if trimmed, isDetail := strings.CutPrefix(name, "input."); isDetail && (trimmed == "cached_tokens" || trimmed == "cache_write_tokens") {
				return unsupported("/usage/details/"+name, "detail name is reserved for the canonical counter")
			}
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
		if isSupported {
			continue
		}
		// The provider's TTL buckets have no target field outside Anthropic.
		// Their total survives in CacheCreation, so the projection is recorded
		// rather than failing a response the client can otherwise consume.
		if adapter.omitsCacheCreationBucket(name) {
			options.Diagnostics.Add(p.ConversionIssue{
				Code: p.UnsupportedCapability, Severity: p.SeverityWarning, Protocol: options.Identity(),
				Direction: p.EncodeResponse, Stage: "wire", Path: "/usage/details/" + name,
				Reason:     "cache creation bucket omitted: target has no equivalent usage detail",
				Suggestion: "Compare cache creation through the total counter; the TTL breakdown is provider-specific.",
			})
			continue
		}
		return unsupported("/usage/details/"+name, "target has no equivalent usage detail")
	}
	return nil
}

// omitsCacheCreationBucket reports whether a provider TTL bucket is projected
// away for this target. Anthropic owns the bucket schema; every other family
// keeps only the total creation counter.
func (adapter module) omitsCacheCreationBucket(name string) bool {
	if adapter.name == Anthropic {
		return false
	}
	return name == "ephemeral_5m_input_tokens" || name == "ephemeral_1h_input_tokens"
}
