package protocol

import (
	"crypto/rand"
	"math"
)

type responsesStorageOptions struct {
	TargetCodec   string `json:"targetCodec"`
	OnUnsupported string `json:"onUnsupported"`
}

// ParseResponsesStore is shared by the wire decoder and semantic normalization.
func ParseResponsesStore(raw Value) (bool, error) {
	if raw.IsZero() || raw.IsNull() {
		return true, nil
	}
	var enabled bool
	if err := raw.Decode(&enabled); err != nil {
		return false, streamIssue(InvalidInput, "/store", "store must be a boolean or null")
	}
	return enabled, nil
}

func (c *CompiledConversion) responsesStorage(req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	raw := req.Parameters["store"]
	if req.ClientOutput != nil && req.ClientOutput.ResponsesStorage != nil {
		intent := req.ClientOutput.ResponsesStorage
		if !raw.IsZero() && !equalValues(raw, intent.Raw) {
			return streamIssue(InvalidInput, "/store", "conflicting legacy and typed storage intent")
		}
		raw = intent.Raw
	}
	requested, err := ParseResponsesStore(raw)
	if err != nil {
		return err
	}
	var settings responsesStorageOptions
	if err := rule.Value.Decode(&settings); err != nil {
		return err
	}
	if settings.TargetCodec == "responses" {
		return nil
	}
	if requested {
		check := rule
		if settings.OnUnsupported == "reject" {
			check.Action = "reject"
		}
		if err := c.issue(check, ConversionRequest, route, "/store", "target cannot store a retrievable Responses object; proceeding stateless (store:false). Set store:false explicitly when storage is not required", sink, true); err != nil {
			return err
		}
	}
	delete(req.Parameters, "store")
	if req.ClientOutput == nil {
		req.ClientOutput = &ClientOutput{}
	}
	req.ClientOutput.ResponsesStorage = &ResponsesStorageIntent{Raw: raw, Requested: requested, Effective: false}
	return nil
}

func (c *CompiledConversion) anthropicUsageEnvelope(usage *Usage, phase ConversionPhase, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, base string) (*Usage, error) {
	u := MergeUsage(nil, usage)
	if u == nil {
		u = &Usage{}
	}
	for _, entry := range []struct {
		name    string
		counter **Counter
	}{{"input", &u.Input}, {"output", &u.Output}} {
		if *entry.counter != nil {
			continue
		}
		if err := c.issue(rule, phase, route, base+"/"+entry.name, "Anthropic requires this counter; client-only zero placeholder is not an observed token count. Older clients may retain the initial count", sink, true); err != nil {
			return nil, err
		}
		count := int64(0)
		if entry.name == "input" {
			// Canonical input includes cache; the unknown uncached wire subtotal is zero.
			for _, cached := range []*Counter{u.CacheRead, u.CacheCreation} {
				if cached != nil {
					if cached.Count < 0 || count > math.MaxInt64-cached.Count {
						return nil, streamIssue(InvalidInput, base+"/input", "cache input count overflow")
					}
					count += cached.Count
				}
			}
		}
		*entry.counter = &Counter{Count: count, Origin: PlaceholderCount}
	}
	return MergeUsage(nil, u), nil
}

func (c *CompiledConversion) envelopeUsageValue(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	if phase == ConversionResponse {
		var response Response
		if err := input.Decode(&response); err != nil {
			return Value{}, err
		}
		if !response.Error.IsZero() && !response.Error.IsNull() {
			return input, nil
		}
		ensureDeliveryIdentity(&response, route)
		usage, err := c.anthropicUsageEnvelope(response.Usage, phase, route, rule, sink, "/usage")
		if err != nil {
			return Value{}, err
		}
		response.Usage = usage
		return EncodeValue(response)
	}
	var event Event
	if err := input.Decode(&event); err != nil {
		return Value{}, err
	}
	if event.Type != ResponseStarted && event.Type != ResponseFinished {
		return input, nil
	}
	usage := event.Usage
	if event.Response != nil {
		usage = MergeUsage(usage, event.Response.Usage)
	}
	usage, err := c.anthropicUsageEnvelope(usage, phase, route, rule, sink, "/response/usage")
	if err != nil {
		return Value{}, err
	}
	if event.Response == nil {
		event.Response = &Response{SchemaVersion: SemanticSchemaVersion}
	}
	event.Response.Usage = usage
	return EncodeValue(event)
}

func ensureDeliveryIdentity(response *Response, route ConversionContext) {
	if response.ID.IsZero() || response.ID.IsNull() {
		response.ID = StringValue("msg_" + rand.Text())
	}
	if response.Model.IsZero() || response.Model.IsNull() {
		model := route.Model
		if model == "" {
			model = route.Scope.Model
		}
		if model == "" {
			model = "unknown"
		}
		response.Model = StringValue(model)
	}
}

func (c *CompiledConversion) HasAnthropicEnvelope(phase ConversionPhase, route ConversionContext) bool {
	if c == nil {
		return false
	}
	for _, rule := range c.Policy.Rules {
		if rule.Enabled && rule.Action == "anthropic_usage_envelope" && rule.Phase == phase && rule.Match.matches(route, Value{}) {
			return true
		}
	}
	return false
}
