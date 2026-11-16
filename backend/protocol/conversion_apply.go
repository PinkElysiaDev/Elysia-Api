package protocol

import (
	"context"
	"reflect"
)

// Request applies ingress and request rules to an owned result, then resolves
// gateway usage settings. ApplyValue executes only its selected rule phase.
func (c *CompiledConversion) Request(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	if request != nil && request.continuationInput != nil && !request.continuationsAccepted {
		return nil, operationMetadataError("/continuation", "selected continuation carriers have not been authenticated")
	}
	if c != nil {
		if err := checkSemanticValue(request, c.phaseLimits(ctx, ConversionRequest)); err != nil {
			return nil, resourceIssue(err, route.Source, DecodeRequest, "conversion.request")
		}
	}
	if request != nil && c.canOwnRequest() {
		return c.convertOwnedRequest(ctx, request, route, sink)
	}
	v, err := EncodeSemantic(ctx, DecodeRequest, request)
	if err != nil {
		return nil, err
	}
	v, err = c.ApplyValue(ctx, ConversionIngress, v, route, sink)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionRequest, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Request
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	if request != nil {
		result.inheritContinuations(request)
	}
	applyClientOutputSettings(&result, c.Policy.Usage)
	return &result, nil
}

// JSON-dependent authored rules retain the value boundary. This selection is
// made before executing any rule, so errors never cause a second execution.
func (c *CompiledConversion) canOwnRequest() bool {
	if c == nil {
		return false
	}
	return !c.HasPhase(ConversionIngress) && c.canOwnPhase(ConversionRequest)
}

func (c *CompiledConversion) convertOwnedRequest(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	plan := typedLimitPlans[reflect.TypeFor[*Request]()]
	owned := cloneSemantic(reflect.ValueOf(request), plan).Interface().(*Request)
	limits := c.phaseLimits(ctx, ConversionRequest)
	state := conversionState{document: owned, limits: limits, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: limits}}
	state.cache.normalizeDocument(reflect.ValueOf(owned), plan)
	if _, err := c.applyConversionState(ctx, ConversionIngress, Value{}, route, sink, &state, false); err != nil {
		return nil, err
	}
	if _, err := c.applyConversionState(ctx, ConversionRequest, Value{}, route, sink, &state, false); err != nil {
		return nil, err
	}
	applyClientOutputSettings(owned, c.Policy.Usage)
	return owned, nil
}

func applyClientOutputSettings(request *Request, settings *ConversionUsageSettings) {
	if settings == nil {
		return
	}
	if request.ClientOutput == nil {
		request.ClientOutput = &ClientOutput{}
	}
	output := request.ClientOutput
	output.CollectUsage = settings.CollectUpstreamUsage
	if settings.DefaultIncludeUsage && output.IncludeUsage == nil {
		includeUsage := true
		output.IncludeUsage = &includeUsage
	}
}

func (c *CompiledConversion) Response(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	limits := semanticLimits(DefaultLimits())
	if c != nil {
		limits = c.phaseLimits(ctx, ConversionResponse)
	}
	if err := checkSemanticValue(response, limits); err != nil {
		return nil, err
	}
	owned := normalizeMappingSemantic(response, limits)
	bindRangeAssociations(owned)
	state := conversionState{document: owned, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: limits}, limits: limits}
	if _, err := c.applyConversionState(ctx, ConversionResponse, Value{}, route, sink, &state, false); err != nil {
		return nil, err
	}
	if err := state.load(ConversionResponse, true); err != nil {
		return nil, err
	}
	result := state.document.(*Response)
	inheritResponseRangeOrigin(response, result)
	return result, nil
}

func (c *CompiledConversion) Event(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (result Event, err error) {
	if event.prelude != nil && (c == nil || event.prelude.PolicyHash != c.Hash || event.prelude.Protocol != route.Target) {
		return Event{}, streamIssue(InvalidAssociation, "/response", "generated response header belongs to a different target policy")
	}
	origin := event.rangeOrigin
	defer func() {
		if err == nil && event.prelude != nil {
			sink.Add(*event.prelude)
		}
		if err == nil && event.SchemaVersion == ProtocolCoreVersion {
			inheritResponseRangeOrigin(event.Response, result.Response)
			if origin == nil && !sameEventRangeInput(&event, &result) {
				origin = captureEventRangeOrigin(event)
			}
			result.rangeOrigin = origin
		}
	}()
	entity := event.continuation
	if entity == nil {
		return c.convertEvent(ctx, event, route, sink)
	}
	route.continuationEvent = &continuationEventObservation{original: event, entity: entity}
	result, err = c.convertEvent(ctx, event, route, sink)
	if err != nil {
		return Event{}, err
	}
	if !sameContinuationEvent(event, result) {
		entity.isChanged = true
		if entity.isRecovering {
			return Event{}, operationMetadataError("/continuation", "conversion changed content bound to authenticated continuation")
		}
	}
	result.continuation = entity
	return result, nil
}

func (c *CompiledConversion) convertEvent(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (Event, error) {
	if event.auxiliary != nil || event.omittedAuxiliary != nil || event.controlReceipt != nil || event.omittedControl != nil {
		return c.convertProtectedEvent(ctx, event, route, sink)
	}
	if c != nil {
		budget := semanticLimits(DefaultLimits())
		if err := checkSemanticValue(event, budget); err != nil {
			return Event{}, err
		}
		owned := normalizeMappingSemantic(&event, budget)
		bindRangeAssociations(owned)
		state := conversionState{document: owned, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: budget}}
		if _, err := c.applyConversionState(ctx, ConversionEvent, Value{}, route, sink, &state, false); err != nil {
			return Event{}, err
		}
		if err := state.load(ConversionEvent, true); err != nil {
			return Event{}, err
		}
		return *state.document.(*Event), nil
	}
	v, err := EncodeSemantic(ctx, DecodeEvent, event)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionEvent, v, route, sink)
	}
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = decodeContract(v.Bytes(), &result)
	return result, err
}

func (c *CompiledConversion) canOwnPhase(phase ConversionPhase) bool {
	return c.ownedPhases[phase]
}
