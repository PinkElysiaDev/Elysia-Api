package protocol

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

// DecodeRequest executes this revision's request decoder, stamps its source
// identity and validates actual requested capabilities before routing.
func (compiled *Compiled) DecodeRequest(ctx context.Context, body []byte, options EvaluationContext) (*Request, error) {
	return compiled.decodeRequest(ctx, body, options, nil)
}

func (compiled *Compiled) decodeRequest(ctx context.Context, body []byte, options EvaluationContext, boundScope *Scope) (result *Request, err error) {
	input, err := ParseValue(body)
	if err != nil {
		return nil, compiled.runtimeError(DecodeRequest, err)
	}
	return compiled.decodeRequestValue(ctx, input, options, boundScope)
}

// DecodeRequestValue accepts an already validated immutable wire document.
// Only syntax validation/copying is reused; scopes, semantic/mapping budgets,
// schemas, capability checks and candidate-owned containers are still evaluated.
func (compiled *Compiled) DecodeRequestValue(ctx context.Context, input Value, options EvaluationContext) (*Request, error) {
	return compiled.decodeRequestValue(ctx, input, options, nil)
}

func (compiled *Compiled) decodeRequestValue(ctx context.Context, input Value, options EvaluationContext, boundScope *Scope) (result *Request, err error) {
	defer func() { err = compiled.runtimeError(DecodeRequest, err) }()
	ctx = withDocumentMetrics(ctx, input.Len())
	if input.IsZero() {
		return nil, fmt.Errorf("invalid JSON value")
	}
	if compiled.packagePlan != nil {
		var err error
		options, _, err = compiled.ExtractContinuations(ctx, input, options)
		if err != nil {
			return nil, err
		}
	}
	if compiled.native.Preserve || compiled.mappings[DecodeRequest].hasCoreOutputPlan {
		options.valueReader = requestValueReader(input)
	}
	request, err := decodeTyped(ctx, compiled, DecodeRequest, input, options, func(module TypedModule, options EvaluationContext) (*Request, error) {
		return module.DecodeRequest(ctx, input, options)
	})
	if err != nil {
		return nil, err
	}
	request.continuationInput = options.continuationInput

	var baseline *Request
	if compiled.native.Preserve && compiled.supportsRequestWorkspace() {
		baseline = cloneRequestSemantic(request)
	}
	request, err = compiled.finishDecodedRequest(ctx, input, request, options, boundScope)
	if err == nil && baseline != nil {
		compiled.attachRequestWorkspace(ctx, request, baseline)
	}
	return request, err
}

func (compiled *Compiled) finishDecodedRequest(ctx context.Context, input Value, request *Request, options EvaluationContext, boundScope *Scope) (*Request, error) {
	sourceContext := &NativeRequestContext{Scope: options.Scope, Values: maps.Clone(options.Values), InterleavedThinking: options.InterleavedThinking}
	if compiled.packagePlan == nil {
		request.SchemaVersion = SemanticSchemaVersion
	}
	if options.valueReader != nil {
		detachRequestResources(request)
	}
	request.Source = compiled.identity
	if codec := compiled.Codec(DecodeRequest); knownConversionCodec(codec) {
		// Normalization replaces only Parameters/Reasoning and clones parameters
		// before editing them; history and tool containers are read-only here.
		validation := *request
		if err := NormalizeReasoningParameters(&validation, codec, options.InterleavedThinking); err != nil {
			return nil, err
		}
		if err := validateSourceReasoning(codec, validation.Reasoning); err != nil {
			return nil, err
		}
		if codec == "openai-chat" {
			if _, err := ParseResponsesStore(request.Parameters["store"]); err != nil {
				return nil, err
			}
		}
	}
	if err := compiled.checkDecodedResponsesContext(request); err != nil {
		return nil, err
	}
	if boundScope != nil {
		options.Scope = *boundScope
	} else if options.ResolveRequestScope != nil {
		scope, err := options.ResolveRequestScope(request)
		if err != nil {
			return nil, err
		}
		options.Scope = scope
	}
	request.Native = nil
	if compiled.native.Preserve {
		request.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: DecodeRequest, Scope: options.Scope}, Value: input, RequestContext: sourceContext}
	}
	if err := stampResourceScopes(request, options.Scope); err != nil {
		return nil, err
	}
	compiled.stampRequestProvenance(request, options.Scope)
	if err := checkDocumentSemantic(ctx, request, semanticLimits(compiled.wireLimits(ctx, DecodeRequest))); err != nil {
		return nil, err
	}
	target := compiled.target(DecodeRequest, options)
	target.Direction = EncodeRequest
	if err := IssuesError(CheckRequest(request, target, semanticLimits(compiled.nativeLimits(ctx, EncodeRequest)))); err != nil {
		return nil, err
	}
	return request, nil
}

// EncodeRequest validates capabilities and evaluates only the author's declared
// mapping. Native replay and explicit edits are handled by the preservation
// layer; unknown fields are never copied to a foreign protocol by this method.
func (compiled *Compiled) EncodeRequest(ctx context.Context, request *Request, options EvaluationContext) (result []byte, err error) {
	output, err := compiled.EncodeRequestValue(ctx, request, options)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// EncodeRequestValue retains the owned immutable result through internal
// validation boundaries. Public byte encoding still returns an independent copy.
func (compiled *Compiled) EncodeRequestValue(ctx context.Context, request *Request, options EvaluationContext) (result Value, err error) {
	defer func() { err = compiled.runtimeError(EncodeRequest, err) }()
	if request != nil && request.Native != nil {
		ctx = withDocumentMetrics(ctx, request.Native.Value.Len())
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	inputLimits, _, _ := compiled.mappingLimits(ctx, EncodeRequest)
	if err := checkDocumentSemantic(ctx, request, inputLimits); err != nil {
		return Value{}, err
	}
	if err := validateRequestContext(request); err != nil {
		return Value{}, err
	}
	if request != nil && request.continuationInput != nil {
		if !request.continuationsAccepted {
			return Value{}, operationMetadataError("/continuation", "selected continuation carriers have not been authenticated")
		}
		if native := request.Native; native != nil && (native.Value != request.continuationInput.input || native.Source.Protocol != request.continuationInput.compiled.identity || native.Source.Path != "") {
			return Value{}, operationMetadataError("/continuation", "request native no longer matches its authenticated carrier source")
		}
		options.continuationInput = request.continuationInput
	}
	target := compiled.target(EncodeRequest, options)
	if err := IssuesError(CheckRequest(request, target, semanticLimits(compiled.nativeLimits(ctx, EncodeRequest)))); err != nil {
		return Value{}, err
	}
	if compiled.packagePlan != nil && (compiled.packagePlan.hasFieldProjection || compiled.packagePlan.hasCacheProjection) && compiled.native.Preserve && request.Native != nil && CanPreserveNative(request.Native.Source, target) {
		options.requestReplay = newRequestReplay(compiled, options)
	}
	output, err := encodeTyped(ctx, compiled, EncodeRequest, request, options, func(module TypedModule, input *Request, options EvaluationContext) (Value, error) {
		return module.EncodeRequest(ctx, input, options)
	})
	if err != nil {
		return Value{}, err
	}
	output, err = compiled.preserveMappedNative(ctx, EncodeRequest, request.Native, output, options)
	if err != nil {
		return Value{}, err
	}
	return output, nil
}

// DecodeResponse executes the independent upstream response mapping.
func (compiled *Compiled) DecodeResponse(ctx context.Context, body []byte, options EvaluationContext) (*Response, error) {
	input, err := ParseValue(body)
	if err != nil {
		return nil, compiled.runtimeError(DecodeResponse, err)
	}
	return compiled.DecodeResponseValue(ctx, input, options)
}

// DecodeResponseValue consumes immutable wire without a second transport copy.
func (compiled *Compiled) DecodeResponseValue(ctx context.Context, input Value, options EvaluationContext) (result *Response, err error) {
	defer func() { err = compiled.runtimeError(DecodeResponse, err) }()
	response, err := decodeTyped(ctx, compiled, DecodeResponse, input, options, func(module TypedModule, options EvaluationContext) (*Response, error) {
		return module.DecodeResponse(ctx, input, options)
	})
	if err != nil {
		return nil, err
	}
	if compiled.packagePlan == nil {
		response.SchemaVersion = SemanticSchemaVersion
	}
	response.Source = compiled.identity
	if compiled.mappings[DecodeResponse].hasCoreOutputPlan {
		detachNodeResources(response.Content)
	}
	response.Native = nil
	if compiled.native.Preserve {
		response.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: DecodeResponse, Scope: options.Scope}, Value: input}
	}
	if err := stampNodeScopes(response.Content, options.Scope); err != nil {
		return nil, err
	}
	compiled.stampNodeProvenance(response.Content, DecodeResponse, options.Scope)
	target := compiled.target(DecodeResponse, options)
	target.Direction = EncodeResponse
	if err := IssuesError(CheckResponse(response, target, compiled.limits)); err != nil {
		return nil, err
	}
	return response, nil
}

// EncodeResponse executes the independently authored client response mapping.
func (compiled *Compiled) EncodeResponse(ctx context.Context, response *Response, options EvaluationContext) (result []byte, err error) {
	value, err := compiled.EncodeResponseValue(ctx, response, options)
	if err != nil {
		return nil, err
	}
	return value.Bytes(), nil
}

// EncodeResponseValue keeps the immutable response through carrier and final
// delivery validation; byte encoding remains the public owned-copy boundary.
func (compiled *Compiled) EncodeResponseValue(ctx context.Context, response *Response, options EvaluationContext) (result Value, err error) {
	defer func() { err = compiled.runtimeError(EncodeResponse, err) }()
	if err := IssuesError(CheckResponse(response, compiled.target(EncodeResponse, options), compiled.limits)); err != nil {
		return Value{}, err
	}
	if err := compiled.ValidateResponseReferences(ctx, response); err != nil {
		return Value{}, err
	}
	output, err := encodeTyped(ctx, compiled, EncodeResponse, response, options, func(module TypedModule, input *Response, options EvaluationContext) (Value, error) {
		return module.EncodeResponse(ctx, input, options)
	})
	if err != nil {
		return Value{}, err
	}
	output, err = compiled.preserveMappedNative(ctx, EncodeResponse, response.Native, output, options)
	if err != nil {
		return Value{}, err
	}
	return output, nil
}

// DecodeEvents maps a wire frame to ordered semantic events. Lifecycle and
// association validation belongs to one per-stream state, outside this immutable
// compiled object.
func (compiled *Compiled) DecodeEvents(ctx context.Context, frame Value, options EvaluationContext) ([]Event, error) {
	return compiled.decodeEvents(ctx, DecodeEvent, frame, options)
}

// DecodeClientEvents decodes the independent client-to-upstream session lane.
func (compiled *Compiled) DecodeClientEvents(ctx context.Context, frame Value, options EvaluationContext) ([]Event, error) {
	return compiled.decodeEvents(ctx, DecodeClientEvent, frame, options)
}

func (compiled *Compiled) decodeEvents(ctx context.Context, direction Direction, frame Value, options EvaluationContext) (result []Event, err error) {
	options, transaction, err := compiled.beginPackageTransaction(direction, options)
	if err != nil {
		return nil, compiled.runtimeError(direction, err)
	}
	defer func() { transaction.finish(err) }()
	defer func() { err = compiled.runtimeError(direction, err) }()
	if options.transportControl == nil {
		options.transportControl = new(bool)
	}
	events, err := compiled.decodeEventValues(ctx, direction, frame, options)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 && !options.compoundFrame && !*options.transportControl && !compiled.mappings[direction].canDeferEvents {
		return nil, streamIssue(UpstreamContractViolation, "/events", "a wire event must produce at least one semantic event")
	}
	if compiled.native.Preserve && len(events) > 1 && !options.compoundFrame {
		return nil, IssuesError([]ConversionIssue{{Code: UnsupportedNative, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "decode", Path: "/directions/" + string(direction), Reason: "native frame preservation requires a single semantic event per frame", Suggestion: "Use a compound response/item event or disable native replay and verify the explicit event mappings."}})
	}
	for index := range events {
		event := &events[index]
		if compiled.packagePlan == nil {
			event.SchemaVersion = SemanticSchemaVersion
		}
		event.Source = compiled.identity
		if compiled.mappings[direction].hasCoreOutputPlan {
			detachEventResources(event)
		}
		if err := compiled.stampEventProvenance(event, direction, options.Scope); err != nil {
			return nil, err
		}
		event.Native = nil
		isAuxiliary := options.inputView != nil && options.inputView.sourceReceipt != nil && options.inputView.sourceReceipt.class == wireFrameAuxiliary
		if isAuxiliary || compiled.native.Preserve && !*options.transportControl && (!options.compoundFrame || event.Type == NativeEvent) {
			event.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: direction, Scope: options.Scope}, Value: frame}
			// Only an installed Responses decoder can designate this native
			// envelope as auxiliary metadata. Authored labels are insufficient.
			if compiled.packagePlan == nil && event.Type == NativeEvent && direction == DecodeEvent && compiled.Codec(direction) == "responses" {
				kind, _ := readValuePointer(frame, []string{"type"})
				if kind.IsString("response.metadata") && ValidateResponsesMetadataFrame(frame) == nil {
					event.Native.Source.Path = responsesMetadataPath
				}
			}
		}
		if isAuxiliary {
			if len(events) != 1 {
				return nil, streamIssue(UnsupportedNative, "/events", "one auxiliary frame must map to one isolated native event")
			}
			if err := options.inputView.sourceReceipt.attach(event); err != nil {
				return nil, err
			}
		}
		if options.inputView != nil && options.inputView.sourceReceipt != nil && options.inputView.sourceReceipt.class == wireFrameState {
			if len(events) != 1 {
				return nil, streamIssue(UnsupportedNative, "/events", "one state frame must map to one isolated control event")
			}
			if err := options.inputView.sourceReceipt.attachControl(event, options.inputView); err != nil {
				return nil, err
			}
		}
		target := compiled.target(direction, options)
		target.Direction = eventEncoder(direction)
		if err := IssuesError(CheckEvent(*event, target, compiled.limits)); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func (compiled *Compiled) stampRequestProvenance(request *Request, scope Scope) {
	compiled.stampNodeProvenance(request.Content, DecodeRequest, scope)
	for index := range request.Tools {
		compiled.stampNative(request.Tools[index].Native, DecodeRequest, scope)
	}
}

func (compiled *Compiled) stampNodeProvenance(nodes []Node, direction Direction, scope Scope) {
	for index := range nodes {
		compiled.stampNodeOrigin(&nodes[index], direction, scope)
		compiled.stampNodeProvenance(nodes[index].Children, direction, scope)
		compiled.stampNodeProvenance(nodes[index].ReasoningContent, direction, scope)
	}
}

func (compiled *Compiled) stampNodeOrigin(node *Node, direction Direction, scope Scope) {
	compiled.stampNative(node.Native, direction, scope)
	if node.Source != nil {
		origin := *node.Source
		origin.Protocol, origin.Direction, origin.Scope = compiled.identity, direction, scope
		node.Source = &origin
	} else if node.Native != nil {
		origin := node.Native.Source
		node.Source = &origin
	}
}

func (compiled *Compiled) stampNative(native *Native, direction Direction, scope Scope) {
	if native != nil {
		native.Source.Protocol, native.Source.Direction, native.Source.Scope = compiled.identity, direction, scope
	}
}

func (compiled *Compiled) stampEventProvenance(event *Event, direction Direction, scope Scope) error {
	if state := event.ResponsesTurnState; state != nil {
		state.Source = Provenance{Protocol: compiled.identity, Direction: direction, Scope: scope, Path: ResponsesTurnStatePath}
	}
	compiled.stampNative(event.Unmapped, direction, scope)
	if event.Request != nil {
		if compiled.packagePlan == nil {
			event.Request.SchemaVersion = SemanticSchemaVersion
		}
		event.Request.Source = compiled.identity
		compiled.stampNative(event.Request.Native, direction, scope)
		if err := stampResourceScopes(event.Request, scope); err != nil {
			return err
		}
		compiled.stampNodeProvenance(event.Request.Content, direction, scope)
		for index := range event.Request.Tools {
			compiled.stampNative(event.Request.Tools[index].Native, direction, scope)
		}
	}
	if event.Item != nil {
		if err := stampNodeScope(event.Item, scope); err != nil {
			return err
		}
		compiled.stampNodeOrigin(event.Item, direction, scope)
		compiled.stampNodeProvenance(event.Item.Children, direction, scope)
		compiled.stampNodeProvenance(event.Item.ReasoningContent, direction, scope)
	}
	if event.Response != nil {
		if compiled.packagePlan == nil {
			event.Response.SchemaVersion = SemanticSchemaVersion
		}
		event.Response.Source = compiled.identity
		compiled.stampNative(event.Response.Native, direction, scope)
		if err := stampNodeScopes(event.Response.Content, scope); err != nil {
			return err
		}
		compiled.stampNodeProvenance(event.Response.Content, direction, scope)
	}
	if event.Media != nil {
		return stampResourceScope(&event.Media.Reference, scope)
	}
	return nil
}

// EncodeEvent emits a declared event body; transport framing remains separate.
func (compiled *Compiled) EncodeEvent(ctx context.Context, event Event, options EvaluationContext) (Value, error) {
	return compiled.encodeEvent(ctx, EncodeEvent, event, options)
}

// EncodeUpstreamEvent encodes a validated client session event for the upstream.
func (compiled *Compiled) EncodeUpstreamEvent(ctx context.Context, event Event, options EvaluationContext) (Value, error) {
	return compiled.encodeEvent(ctx, EncodeUpstreamEvent, event, options)
}

func (compiled *Compiled) encodeEvent(ctx context.Context, direction Direction, event Event, options EvaluationContext) (result Value, err error) {
	options, transaction, err := compiled.beginPackageTransaction(direction, options)
	if err != nil {
		return Value{}, compiled.runtimeError(direction, err)
	}
	defer func() { transaction.finish(err) }()
	defer func() { err = compiled.runtimeError(direction, err) }()
	if err := IssuesError(CheckEvent(event, compiled.target(direction, options), compiled.limits)); err != nil {
		return Value{}, err
	}
	output, err := compiled.encodeEventValue(ctx, direction, event, options)
	if err != nil {
		return Value{}, err
	}
	return compiled.preserveMappedNative(ctx, direction, event.Native, output, options)
}

func (compiled *Compiled) runtimeError(direction Direction, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	err = resourceIssue(err, compiled.identity, direction, "runtime")
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		issues := append([]ConversionIssue(nil), conversion.Issues...)
		for index := range issues {
			if issues[index].Protocol.DefinitionID == "" {
				issues[index].Protocol = compiled.identity
			}
			if issues[index].Direction == "" {
				issues[index].Direction = direction
			}
		}
		annotated := &ConversionError{cause: err, Issues: issues, CandidateOnly: conversion.CandidateOnly}
		var failure *GenerationFailure
		if errors.As(err, &failure) {
			return errors.Join(failure, annotated)
		}
		return annotated
	}
	code := InvalidInput
	if direction == DecodeResponse || direction == DecodeEvent {
		code = UpstreamContractViolation
	}
	return &ConversionError{cause: err, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "runtime", Path: "/", Reason: err.Error(), Suggestion: "Correct the wire input or semantic mapping and verify the revision again."}}}
}

// AnnotateSourceEventError attaches the pinned source to replay errors raised
// outside the decoder, so EOF failures retain the same diagnostic provenance.
func (compiled *Compiled) AnnotateSourceEventError(err error) error {
	return compiled.runtimeError(DecodeEvent, err)
}

func stampResourceScopes(request *Request, scope Scope) error {
	for index := range request.Resources {
		if err := stampResourceScope(&request.Resources[index], scope); err != nil {
			return err
		}
	}
	if err := stampCacheScopes(request.Cache, scope); err != nil {
		return err
	}
	for index := range request.Tools {
		if err := stampCacheScopes(request.Tools[index].Cache, scope); err != nil {
			return err
		}
	}
	return stampNodeScopes(request.Content, scope)
}

func stampNodeScopes(nodes []Node, scope Scope) error {
	for index := range nodes {
		if err := stampNodeScope(&nodes[index], scope); err != nil {
			return err
		}
	}
	return nil
}

func stampNodeScope(node *Node, scope Scope) error {
	for index := range node.Resources {
		if err := stampResourceScope(&node.Resources[index], scope); err != nil {
			return err
		}
	}
	if err := stampCacheScopes(node.Cache, scope); err != nil {
		return err
	}
	if err := stampNodeScopes(node.Children, scope); err != nil {
		return err
	}
	return stampNodeScopes(node.ReasoningContent, scope)
}

func stampCacheScopes(intents []CacheIntent, scope Scope) error {
	for index := range intents {
		if intents[index].Resource != nil {
			if err := stampResourceScope(intents[index].Resource, scope); err != nil {
				return err
			}
		}
	}
	return nil
}

func stampResourceScope(resource *Resource, scope Scope) error {
	if resource.Scope != (Scope{}) && resource.Scope != scope {
		return fmt.Errorf("resource_scope_mismatch: mapping cannot replace gateway-owned scope")
	}
	resource.Scope = scope
	return nil
}

func (compiled *Compiled) target(direction Direction, options EvaluationContext) Target {
	target := Target{Codec: compiled.Codec(direction), Protocol: compiled.identity, Direction: direction, Scope: options.Scope, Capabilities: compiled.mappings[direction].capabilities, coreVersion: compiled.CoreVersion()}
	if isEventDirection(direction) {
		target.eventEncoderHash = compiled.mappings[eventEncoder(direction)].definitionHash
		target.eventDecoderHash = compiled.mappings[eventDecoder(direction)].definitionHash
	}
	return target
}

// Target binds validation and collection to this revision's core version and
// capabilities. Callers may narrow Capabilities to their authorized binding.
func (compiled *Compiled) Target(direction Direction, scope Scope) Target {
	return compiled.TargetWithCapabilities(direction, scope, compiled.Capabilities(direction))
}

// TargetWithCapabilities binds this revision's core version and direction while
// borrowing the caller's fixed, read-only capability map for the target's lifetime.
// It neither copies the map nor supplies default capabilities when it is nil.
func (compiled *Compiled) TargetWithCapabilities(direction Direction, scope Scope, capabilities CapabilitySet) Target {
	target := compiled.target(direction, EvaluationContext{Scope: scope})
	target.Capabilities = capabilities
	return target
}

func (compiled *Compiled) preserveMappedNative(ctx context.Context, direction Direction, native *Native, output Value, options EvaluationContext) (Value, error) {
	// Reconciliation replays a decoder with an already pinned binding. It must
	// never repeat authorization/routing or choose a new source account.
	options.ResolveRequestScope = nil
	target := compiled.target(direction, options)
	if !compiled.native.Preserve || native == nil || !CanPreserveNative(native.Source, target) {
		return output, nil
	}
	var replay *Compiled
	var original Value
	var baseline any
	var err error
	if direction == EncodeRequest {
		var prepared preparedRequestReplay
		if options.requestReplay != nil {
			prepared, err = options.requestReplay.load(ctx, native)
		} else {
			prepared, err = compiled.prepareRequestReplay(ctx, native, options)
		}
		if err != nil {
			return Value{}, err
		}
		original, replay, baseline = prepared.original, prepared.compiled, prepared.baseline
		currentReplay := options.requestReplay
		options = prepared.options
		options.requestReplay = currentReplay
	} else {
		original, err = ReplayNative(*native, target, nil, compiled.wireLimits(ctx, direction))
		if err != nil {
			return Value{}, err
		}
		view := *compiled
		replay = &view
		switch direction {
		case EncodeResponse:
			if replay.packagePlan != nil {
				// The baseline describes the original wire. Caller preferences
				// belong only to the already-rendered current response.
				options.ClientOutput = nil
			}
			response, err := compiled.DecodeResponse(ctx, original.Bytes(), options)
			if err != nil {
				return Value{}, err
			}
			baseline = response
		case EncodeEvent, EncodeUpstreamEvent:
			events, err := compiled.decodeEvents(ctx, eventDecoder(direction), original, options)
			if err != nil {
				return Value{}, err
			}
			if len(events) != 1 {
				return Value{}, fmt.Errorf("native frame must map to exactly one event")
			}
			if replay.packagePlan != nil {
				baseline = &events[0]
			} else {
				baseline = events[0]
			}
		default:
			return Value{}, fmt.Errorf("native reconciliation requires a request or response direction")
		}
	}

	var before Value
	if replay.packagePlan != nil {
		before, err = encodePlannedCore(ctx, replay, direction, baseline, options)
	} else if direction == EncodeRequest && replay.hasTypedRequestMapping(EncodeRequest) {
		before, err = encodeRequestMapping(ctx, replay, baseline.(*Request), options, replay.mappings[direction].module.(TypedModule), func(module TypedModule, input *Request, options EvaluationContext) (Value, error) {
			return module.EncodeRequest(ctx, input, options)
		})
	} else {
		var value Value
		value, err = EncodeSemantic(ctx, direction, baseline)
		if err == nil {
			before, err = replay.executeMapping(ctx, direction, value, options)
		}
	}
	if err != nil {
		return Value{}, err
	}

	result, err := reconcileNative(ctx, original, before, output, compiled.native, compiled.wireLimits(ctx, direction))
	if err != nil {
		var resource *ResourceLimitError
		if errors.As(err, &resource) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Value{}, resourceIssue(err, compiled.identity, direction, "preserve")
		}
		return Value{}, IssuesError([]ConversionIssue{{Code: InvalidMutation, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "preserve", Path: native.Source.Path, Reason: err.Error(), Suggestion: "Declare stable array identities or apply an explicit native replacement."}})
	}
	if compiled.packagePlan != nil {
		if err := compiled.validateWireContract(ctx, direction, result, nil, compiled.wireLimits(ctx, direction)); err != nil {
			return Value{}, err
		}
	}
	return result, nil
}
