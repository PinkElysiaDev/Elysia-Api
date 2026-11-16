package protocol

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
	if phase == ConversionResponse || phase == ConversionEvent || phase == ConversionSourceEvent {
		ingress, upstream = target, source
	}
	layers := []ConversionPolicy{DefaultConversionPolicy(ingress, upstream)}
	if policy != nil {
		layers = append(layers, *policy)
	}
	c, err := ResolveConversion(layers...)
	return c, route, err
}

// DefaultConversionPolicy combines source input rules and target output rules.
// Usage collection is gateway intent; each target mapping chooses its wire form.
func DefaultConversionPolicy(ingress, upstream *Compiled) ConversionPolicy {
	p := ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "engine-default", Name: "引擎默认", Mode: "compatible", Rules: []ConversionRule{},
		Continuation: &ContinuationSettings{ClientCarrier: true, Persist: true, RetentionSeconds: 7 * 24 * 3600, TurnsPerSession: 64, MaxBytes: 512 << 20, RecordBytes: 8 << 20}}
	p.Rules = append(p.Rules, upstream.defaultRequestConversionRules(ingress)...)
	p.Rules = append(p.Rules, upstream.defaultConversionRules(ConversionSourceEvent)...)
	p.Rules = append(p.Rules, ingress.defaultConversionRules(ConversionResponse)...)
	p.Rules = append(p.Rules, ingress.defaultConversionRules(ConversionEvent)...)
	if upstream != nil {
		p.Usage = &ConversionUsageSettings{CollectUpstreamUsage: true}
	}
	return p
}

// ValidateUsageArithmetic runs before projection, so a removed subtotal cannot
// hide an invalid upstream result or an invalid authored semantic mapping.
func ValidateUsageArithmetic(usage *Usage) error {
	if usage == nil {
		return nil
	}
	if err := validateExecutionUsage(usage); err != nil {
		return err
	}
	if err := validateModalityUsage(usage); err != nil {
		return err
	}
	if err := validateCacheWriteRetention(usage); err != nil {
		return err
	}
	if !usage.Attribution.IsZero() {
		known, err := ParseUsageAttribution(usage.Attribution)
		if err != nil {
			return err
		}
		if !known {
			return streamIssue(UnsupportedCapability, "/usage/attribution", "unknown attribution shape requires explicit mapping")
		}
	}
	if !usage.CacheBilling.IsZero() {
		known, err := ParseCacheBilling(usage.CacheBilling)
		if err != nil {
			return err
		}
		if !known {
			return streamIssue(UnsupportedCapability, "/usage/cacheBilling", "unknown billing disclosure requires explicit mapping")
		}
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
	return validateToolUsageArithmetic(usage)
}
