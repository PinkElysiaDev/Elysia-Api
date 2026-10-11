package protocol

import (
	"crypto/rand"
	"math"
)

type responsesStorageOptions struct {
	SourceCodec   string `json:"sourceCodec,omitempty"`
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
	var settings responsesStorageOptions
	if err := rule.Value.Decode(&settings); err != nil {
		return err
	}
	if settings.SourceCodec == "" {
		settings.SourceCodec = "responses"
	} // Existing authored rules use Responses defaults.
	raw := req.Parameters["store"]
	intent := &ResponsesStorageIntent{Raw: raw, SourceCodec: settings.SourceCodec}
	if req.ClientOutput != nil && req.ClientOutput.ResponsesStorage != nil {
		previous := req.ClientOutput.ResponsesStorage
		// The outgoing instruction may differ from the original intent on a repeated pass.
		expected, _ := EncodeValue(previous.Effective)
		if !raw.IsZero() && !equalValues(raw, previous.Raw) && (previous.EffectiveUnknown || !equalValues(raw, expected)) {
			return streamIssue(InvalidInput, "/store", "conflicting legacy and typed storage intent")
		}
		*intent = *previous
		raw = intent.Raw
	} else {
		if _, err := ParseResponsesStore(raw); err != nil {
			return err
		}
		switch settings.SourceCodec {
		case "responses":
			intent.Requested, _ = ParseResponsesStore(raw)
		case "openai-chat":
			intent.RequestedUnknown = raw.IsZero() || raw.IsNull()
			if !intent.RequestedUnknown {
				_ = raw.Decode(&intent.Requested)
			}
		}
	}
	intent.Effective, intent.EffectiveUnknown = intent.Requested, intent.RequestedUnknown
	same := settings.SourceCodec == settings.TargetCodec
	supported := settings.TargetCodec == "responses" || settings.TargetCodec == "openai-chat"
	reason := ""
	if !same && intent.RequestedUnknown {
		reason = "source account storage default is unknown; using stateless target instructions"
		intent.Effective, intent.EffectiveUnknown = false, false
	} else if !same && intent.Requested {
		if supported {
			reason = "provider storage remains enabled but the target API has a different retrieval contract"
		} else {
			reason = "target cannot store a retrievable source API object; proceeding stateless (store:false)"
			intent.Effective = false
		}
	}
	if reason != "" {
		check := rule
		if settings.OnUnsupported == "reject" {
			check.Action = "reject"
		}
		if err := c.issue(check, ConversionRequest, route, "/store", reason, sink, true); err != nil {
			if settings.OnUnsupported == "reject" {
				return err
			}
			return candidateFailure(err)
		}
	}
	if req.Parameters == nil {
		req.Parameters = Object{}
	}
	delete(req.Parameters, "store")
	if same {
		if !raw.IsZero() {
			req.Parameters["store"] = raw
		}
	} else if supported {
		req.Parameters["store"], _ = EncodeValue(intent.Effective)
	}
	if req.ClientOutput == nil {
		req.ClientOutput = &ClientOutput{}
	}
	req.ClientOutput.ResponsesStorage = intent
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
