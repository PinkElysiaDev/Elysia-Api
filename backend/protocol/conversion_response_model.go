package protocol

import "context"

// AttributeResponseFailure attributes a failed field contract to the
// provider only when both semantic and final wire values still match the
// observed source. Mapping-created errors retain their target diagnostics.
func (route ConversionContext) AttributeResponseFailure(err error, response *Response, wire Value) error {
	return route.attributeObservedFieldFailure(err, response, wire)
}

// WithResponseObservations captures only the fields declared by both mappings;
// it does not retain the request or response body in conversion context.
func (route ConversionContext) WithResponseObservations(ingress, upstream *Compiled, outbound, source []byte) (ConversionContext, error) {
	route.fieldObservations = nil
	if route.Transport != HTTPJSON || !hasFieldObservationPair(ingress, upstream) {
		return route, nil
	}
	request, err := ParseValue(outbound)
	if err != nil {
		return route, err
	}
	route, err = route.WithRequestObservations(ingress, upstream, request)
	if err != nil {
		return route, err
	}
	return route.WithObservedResponseFields(source)
}

// WithRequestObservations snapshots declared fields from the final outbound wire.
// No request body has to survive provider I/O for later response attribution.
func (route ConversionContext) WithRequestObservations(ingress, upstream *Compiled, outbound Value) (ConversionContext, error) {
	route.fieldObservations = nil
	return route.observeRequestFields(ingress, upstream, outbound)
}

// WithObservedResponseFields completes a dispatch-time snapshot without mutating
// a reusable route. Absence remains distinct from null or an invalid model.
func (route ConversionContext) WithObservedResponseFields(source []byte) (ConversionContext, error) {
	return route.observeResponseFields(source)
}

// ProjectCollectedResponse applies only controls declared for collection.
// Delivery-only response rules do not change the collector's provider semantics.
func (c *CompiledConversion) ProjectCollectedResponse(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	if c == nil {
		return response, nil
	}
	copy := *response
	for _, rule := range c.Policy.Rules {
		if !rule.Enabled || rule.Phase != ConversionResponse || !rule.Match.matches(route, Value{}) {
			continue
		}
		if rule.Action == "core_controls" {
			options, err := readConversionOption[coreControlOptions](c, rule)
			if err != nil {
				return nil, err
			}
			if options.Collection {
				if _, err := c.applySelectedCoreControls(ctx, &copy, route, rule, sink, true); err != nil {
					return nil, err
				}
			}
		}
	}
	return &copy, nil
}
