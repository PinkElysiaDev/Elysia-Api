package protocol

import (
	"fmt"
	"slices"
)

// Codec identifies the installed implementation of one direction, never a
// user-authored family label. An after mapping does not change its wire codec.
func (c *Compiled) Codec(direction Direction) string {
	if c == nil || c.mappings[direction].module == nil {
		return ""
	}
	return c.mappings[direction].module.Name()
}

func knownConversionCodec(codec string) bool {
	return slices.Contains([]string{"openai-chat", "responses", "anthropic", "gemini"}, codec)
}

// ResolvePreviewConversion pins actual endpoints from one runtime view.
func (s *Service) ResolvePreviewConversion(route ConversionContext, phase ConversionPhase, policy *ConversionPolicy) (*CompiledConversion, ConversionContext, error) {
	view := s.View()
	source, sourceOK := view.Pin(route.Source.DefinitionID)
	target, targetOK := view.Pin(route.Target.DefinitionID)
	if !sourceOK || !targetOK {
		return nil, route, streamIssue(InvalidInput, "/context", "preview requires active source and target protocol IDs")
	}
	route.Source, route.Target = source.Identity(), target.Identity()
	created, _ := EncodeValue(1)
	route.Delivery = &DeliveryState{ID: StringValue("response_preview"), Created: created}
	ingress, upstream := source, target
	if phase == ConversionResponse || phase == ConversionEvent {
		ingress, upstream = target, source
	}
	layers := []ConversionPolicy{DefaultConversionPolicy(ingress, upstream)}
	if policy != nil {
		layers = append(layers, *policy)
	}
	c, err := ResolveConversion(layers...)
	return c, route, err
}

// DefaultConversionPolicy applies equally to old custom and shipped definitions.
// Explicitly authored encoders receive no guessed wire projections.
func DefaultConversionPolicy(ingress, upstream *Compiled) ConversionPolicy {
	p := ConversionPolicy{SchemaVersion: 1, ID: "engine-default", Name: "引擎默认", Mode: "compatible", Rules: []ConversionRule{},
		Continuation: &ContinuationSettings{ClientCarrier: true, Persist: true, RetentionSeconds: 7 * 24 * 3600, TurnsPerSession: 64, MaxBytes: 512 << 20, RecordBytes: 8 << 20}}
	if codec := upstream.Codec(EncodeRequest); knownConversionCodec(codec) {
		p.Usage = &ConversionUsageSettings{CollectUpstreamUsage: true}
		p.Rules = append(p.Rules,
			ConversionRule{ID: "history-response-metadata", Order: 160, Enabled: true, Phase: ConversionRequest, Action: "response_metadata", Value: StringValue(codec)},
			ConversionRule{ID: "client-stream-options", Order: 100, Enabled: true, Phase: ConversionRequest, Action: "stream_options"},
			ConversionRule{ID: "responses-include", Order: 180, Enabled: true, Phase: ConversionRequest, Action: "responses_include", Value: StringValue(codec)},
			ConversionRule{ID: "responses-context", Order: 185, Enabled: true, Phase: ConversionRequest, Action: "responses_context", Value: StringValue(codec)},
			ConversionRule{ID: "request-signatures", Order: 200, Enabled: true, Phase: ConversionRequest, Action: "signatures"})
		if ingress.Codec(DecodeRequest) == "responses" {
			settings, _ := EncodeValue(responsesStorageOptions{TargetCodec: codec, OnUnsupported: "degrade"})
			p.Rules = append(p.Rules, ConversionRule{ID: "responses-storage", Order: 190, Enabled: true, Phase: ConversionRequest, Action: "responses_storage", Value: settings})
		}
		if codec == "anthropic" || codec == "gemini" {
			p.Rules = append(p.Rules, ConversionRule{ID: "system-instruction-hoist", Order: 140, Enabled: true, Phase: ConversionRequest, Action: "system_instruction_hoist"})
		}
		if codec != "gemini" {
			p.Rules = append(p.Rules, ConversionRule{ID: "text-tool-results", Order: 150, Enabled: true, Phase: ConversionRequest, Match: ConversionMatch{NodeKind: ToolResultNode}, Action: "tool_result_text"})
		}
		if codec == "gemini" {
			p.Rules = append(p.Rules, ConversionRule{ID: "gemini-tool-results", Order: 150, Enabled: true, Phase: ConversionRequest, Match: ConversionMatch{NodeKind: ToolResultNode}, Action: "tool_result_object", Value: StringValue("result")})
		}
	}
	for _, entry := range []struct {
		direction Direction
		phase     ConversionPhase
		prefix    string
	}{{EncodeResponse, ConversionResponse, "response"}, {EncodeEvent, ConversionEvent, "event"}} {
		if codec := ingress.Codec(entry.direction); knownConversionCodec(codec) {
			if !nativeConversionPair(ingress, upstream, entry.direction) {
				p.Rules = append(p.Rules, ConversionRule{ID: entry.prefix + "-shape", Order: 230, Enabled: true, Phase: entry.phase, Action: "response_shape", Value: StringValue(codec)})
				p.Rules = append(p.Rules, ConversionRule{ID: entry.prefix + "-envelope", Order: 320, Enabled: true, Phase: entry.phase, Action: "response_envelope", Value: StringValue(codec)})
			}
			p.Rules = append(p.Rules,
				ConversionRule{ID: entry.prefix + "-metadata", Order: 220, Enabled: true, Phase: entry.phase, Action: "response_metadata", Value: StringValue(codec)},
				ConversionRule{ID: entry.prefix + "-signatures", Order: 200, Enabled: true, Phase: entry.phase, Action: "signatures"},
				ConversionRule{ID: entry.prefix + "-usage-projection", Order: 300, Enabled: true, Phase: entry.phase, Action: "usage_projection", Value: StringValue(codec)})
			if codec == "anthropic" && !nativeConversionPair(ingress, upstream, entry.direction) {
				p.Rules = append(p.Rules, ConversionRule{ID: entry.prefix + "-anthropic-usage-envelope", Order: 310, Enabled: true, Phase: entry.phase, Action: "anthropic_usage_envelope"})
			}
		}
	}
	return p
}

// Native replay already has its own complete envelope. Only identical installed
// mappings qualify; family labels alone say nothing about custom mixed codecs.
// Final wire validation still rejects malformed native frames.
func nativeConversionPair(ingress, upstream *Compiled, encode Direction) bool {
	decode := DecodeResponse
	if encode == EncodeEvent {
		decode = DecodeEvent
	}
	return ingress != nil && upstream != nil && ingress.native.Preserve && upstream.native.Preserve &&
		ingress.identity.Family == upstream.identity.Family && ingress.identity.WireVersion == upstream.identity.WireVersion &&
		ingress.mappings[encode].definitionHash == upstream.mappings[encode].definitionHash &&
		ingress.mappings[decode].definitionHash == upstream.mappings[decode].definitionHash
}

// ParseResponsesInclude preserves null/empty at the caller while checking every
// element, including values minted by a custom semantic mapping.
func ParseResponsesInclude(raw Value) ([]string, error) {
	if raw.IsZero() || raw.IsNull() {
		return nil, nil
	}
	invalid := func(at string) error {
		return streamIssue(InvalidInput, at, "include must be an array of strings or null")
	}
	values, err := raw.readArray()
	if err != nil {
		return nil, invalid("/include")
	}
	selectors := make([]string, len(values))
	for i, value := range values {
		if value.IsNull() || value.Decode(&selectors[i]) != nil {
			return nil, invalid(fmt.Sprintf("/include/%d", i))
		}
	}
	return selectors, nil
}

func (o *ClientOutput) RequestsEncryptedReasoning() bool {
	if o == nil {
		return false
	}
	selectors, _ := ParseResponsesInclude(o.RawResponsesInclude)
	return slices.Contains(selectors, "reasoning.encrypted_content")
}

// CheckIncludeCarrier runs only when signed state actually exists.
func (c *CompiledConversion) CheckIncludeCarrier(route ConversionContext, sink *DiagnosticSink) error {
	for _, rule := range c.Policy.Rules {
		if rule.Enabled && rule.Action == "responses_include" {
			return c.issue(rule, ConversionResponse, route, "/include", "requested encrypted reasoning carrier is unavailable; server persistence does not deliver the requested client payload", sink, true)
		}
	}
	return nil
}

func (c *CompiledConversion) responsesInclude(req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	raw := req.Parameters["responses_include"]
	if req.ClientOutput != nil && !req.ClientOutput.RawResponsesInclude.IsZero() {
		if !raw.IsZero() && !equalValues(raw, req.ClientOutput.RawResponsesInclude) {
			return streamIssue(InvalidInput, "/include", "conflicting legacy and typed include preferences")
		}
		raw = req.ClientOutput.RawResponsesInclude
	}
	selectors, err := ParseResponsesInclude(raw)
	if err != nil || raw.IsZero() {
		return err
	}
	var codec string
	_ = rule.Value.Decode(&codec)
	if codec != "responses" {
		for i, selector := range selectors {
			if selector != "reasoning.encrypted_content" {
				return c.issue(ConversionRule{ID: rule.ID, Action: "reject"}, ConversionRequest, route, fmt.Sprintf("/include/%d", i), "target has no equivalent include selector", sink, false)
			}
		}
		delete(req.Parameters, "responses_include")
		c.normalized(rule, route, "/include", "Responses output selection retained as client output preferences; not forwarded as an upstream generation parameter", sink)
	} else {
		if req.Parameters == nil {
			req.Parameters = Object{}
		}
		req.Parameters["responses_include"] = raw
	}
	if req.ClientOutput == nil {
		req.ClientOutput = &ClientOutput{}
	}
	req.ClientOutput.RawResponsesInclude = raw
	return nil
}

// ValidateUsageArithmetic runs before projection, so a removed subtotal cannot
// hide an invalid upstream result or an invalid authored semantic mapping.
func ValidateUsageArithmetic(usage *Usage) error {
	if usage == nil {
		return nil
	}
	if count, ok := usage.Details["uncached_input_tokens"]; ok {
		if usage.Input == nil {
			return streamIssue(InvalidInput, "/usage/details/uncached_input_tokens", "uncached subtotal requires total input")
		}
		expected := usage.Input.Count
		for _, cached := range []*Counter{usage.CacheRead, usage.CacheCreation} {
			if cached != nil {
				expected -= cached.Count
			}
		}
		if expected != count.Count {
			return streamIssue(InvalidInput, "/usage/details/uncached_input_tokens", "uncached subtotal disagrees with normalized input")
		}
	}
	if reasoning, ok := usage.Details["output.reasoning_tokens"]; ok && usage.Output != nil && reasoning.Count > usage.Output.Count {
		return streamIssue(InvalidInput, "/usage/details/output.reasoning_tokens", "reasoning output exceeds total output")
	}
	return nil
}

func (c *CompiledConversion) projectUsage(usage *Usage, phase ConversionPhase, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, base string) error {
	// Validate every original counter, including fields about to be omitted.
	// This also protects previews and values produced by earlier authored rules.
	if err := IssuesError(CheckResponse(&Response{SchemaVersion: SemanticSchemaVersion, Usage: usage}, Target{Protocol: route.Source, Capabilities: CapabilitySet{UsageCapability: true}}, c.limits)); err != nil {
		return err
	}
	if usage == nil {
		return nil
	}
	var codec string
	_ = rule.Value.Decode(&codec)
	for _, name := range sortedKeys(usage.Details) {
		drop := name == "output.reasoning_tokens" && codec == "anthropic" ||
			name == "toolUsePromptTokenCount" && codec != "gemini" ||
			(name == "ephemeral_5m_input_tokens" || name == "ephemeral_1h_input_tokens") && codec != "anthropic"
		if !drop {
			continue
		}
		if err := c.issue(rule, phase, route, base+"/details/"+name, "target cannot express this usage detail; original accounting is retained", sink, true); err != nil {
			return err
		}
		delete(usage.Details, name)
	}
	if codec == "gemini" && usage.CacheCreation != nil {
		if err := c.issue(rule, phase, route, base+"/cacheCreation", "target cannot express cache creation; original accounting is retained", sink, true); err != nil {
			return err
		}
		usage.CacheCreation = nil
		if _, ok := usage.Details["uncached_input_tokens"]; ok {
			if err := c.issue(rule, phase, route, base+"/details/uncached_input_tokens", "subtotal depends on omitted cache creation; original accounting is retained", sink, true); err != nil {
				return err
			}
			delete(usage.Details, "uncached_input_tokens")
		}
	}
	return nil
}

func (c *CompiledConversion) projectUsageValue(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	if phase == ConversionResponse {
		var response Response
		if err := input.Decode(&response); err != nil {
			return Value{}, err
		}
		if err := c.projectUsage(response.Usage, phase, route, rule, sink, "/usage"); err != nil {
			return Value{}, err
		}
		return EncodeValue(response)
	}
	var event Event
	if err := input.Decode(&event); err != nil {
		return Value{}, err
	}
	if err := c.projectUsage(event.Usage, phase, route, rule, sink, "/usage"); err != nil {
		return Value{}, err
	}
	if event.Response != nil {
		if err := c.projectUsage(event.Response.Usage, phase, route, rule, sink, "/response/usage"); err != nil {
			return Value{}, err
		}
	}
	return EncodeValue(event)
}
