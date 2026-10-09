package protocol

import (
	"context"
	"errors"
	"fmt"
)

// DecodeRequest executes this revision's request decoder, stamps its source
// identity and validates actual requested capabilities before routing.
func (compiled *Compiled) DecodeRequest(ctx context.Context, body []byte, options EvaluationContext) (result *Request, err error) {
	defer func() { err = compiled.runtimeError(DecodeRequest, err) }()
	input, err := ParseValue(body)
	if err != nil {
		return nil, err
	}
	request, err := decodeTyped(ctx, compiled, DecodeRequest, input, options, func(module TypedModule, options EvaluationContext) (*Request, error) {
		return module.DecodeRequest(ctx, input, options)
	})
	if err != nil {
		return nil, err
	}
	request.SchemaVersion = SemanticSchemaVersion
	request.Source = compiled.identity
	if err := compiled.checkDecodedResponsesContext(request); err != nil {
		return nil, err
	}
	if options.ResolveRequestScope != nil {
		scope, err := options.ResolveRequestScope(request)
		if err != nil {
			return nil, err
		}
		options.Scope = scope
	}
	request.Native = nil
	if compiled.native.Preserve {
		request.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: DecodeRequest, Scope: options.Scope}, Value: input}
	}
	if err := stampResourceScopes(request, options.Scope); err != nil {
		return nil, err
	}
	compiled.stampRequestProvenance(request, options.Scope)
	target := compiled.target(DecodeRequest, options)
	target.Direction = EncodeRequest
	if err := IssuesError(CheckRequest(request, target, compiled.limits)); err != nil {
		return nil, err
	}
	return request, nil
}

// EncodeRequest validates capabilities and evaluates only the author's declared
// mapping. Native replay and explicit edits are handled by the preservation
// layer; unknown fields are never copied to a foreign protocol by this method.
func (compiled *Compiled) EncodeRequest(ctx context.Context, request *Request, options EvaluationContext) (result []byte, err error) {
	defer func() { err = compiled.runtimeError(EncodeRequest, err) }()
	if err := validateRequestContext(request); err != nil {
		return nil, err
	}
	target := compiled.target(EncodeRequest, options)
	if err := IssuesError(CheckRequest(request, target, compiled.limits)); err != nil {
		return nil, err
	}
	output, err := encodeTyped(ctx, compiled, EncodeRequest, request, options, func(module TypedModule, options EvaluationContext) (Value, error) {
		return module.EncodeRequest(ctx, request, options)
	})
	if err != nil {
		return nil, err
	}
	output, err = compiled.preserveMappedNative(ctx, EncodeRequest, request.Native, output, options)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// DecodeResponse executes the independent upstream response mapping.
func (compiled *Compiled) DecodeResponse(ctx context.Context, body []byte, options EvaluationContext) (result *Response, err error) {
	defer func() { err = compiled.runtimeError(DecodeResponse, err) }()
	input, err := ParseValue(body)
	if err != nil {
		return nil, err
	}
	response, err := decodeTyped(ctx, compiled, DecodeResponse, input, options, func(module TypedModule, options EvaluationContext) (*Response, error) {
		return module.DecodeResponse(ctx, input, options)
	})
	if err != nil {
		return nil, err
	}
	response.SchemaVersion = SemanticSchemaVersion
	response.Source = compiled.identity
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
	defer func() { err = compiled.runtimeError(EncodeResponse, err) }()
	if err := IssuesError(CheckResponse(response, compiled.target(EncodeResponse, options), compiled.limits)); err != nil {
		return nil, err
	}
	output, err := encodeTyped(ctx, compiled, EncodeResponse, response, options, func(module TypedModule, options EvaluationContext) (Value, error) {
		return module.EncodeResponse(ctx, response, options)
	})
	if err != nil {
		return nil, err
	}
	output, err = compiled.preserveMappedNative(ctx, EncodeResponse, response.Native, output, options)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
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
	defer func() { err = compiled.runtimeError(direction, err) }()
	events, err := compiled.decodeEventValues(ctx, direction, frame, options)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 && !options.compoundFrame {
		return nil, streamIssue(UpstreamContractViolation, "/events", "a wire event must produce at least one semantic event")
	}
	if compiled.native.Preserve && len(events) > 1 && !options.compoundFrame {
		return nil, IssuesError([]ConversionIssue{{Code: UnsupportedNative, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "decode", Path: "/directions/" + string(direction), Reason: "native frame preservation requires a single semantic event per frame", Suggestion: "Use a compound response/item event or disable native replay and verify the explicit event mappings."}})
	}
	for index := range events {
		event := &events[index]
		event.SchemaVersion = SemanticSchemaVersion
		event.Source = compiled.identity
		if err := compiled.stampEventProvenance(event, direction, options.Scope); err != nil {
			return nil, err
		}
		event.Native = nil
		if compiled.native.Preserve && (!options.compoundFrame || event.Type == NativeEvent) {
			event.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: direction, Scope: options.Scope}, Value: frame}
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
	}
}

func (compiled *Compiled) stampNodeOrigin(node *Node, direction Direction, scope Scope) {
	compiled.stampNative(node.Native, direction, scope)
	if node.Native != nil {
		origin := node.Native.Source
		node.Source = &origin
	} else if node.Source != nil {
		origin := *node.Source
		origin.Protocol, origin.Direction, origin.Scope = compiled.identity, direction, scope
		node.Source = &origin
	}
}

func (compiled *Compiled) stampNative(native *Native, direction Direction, scope Scope) {
	if native != nil {
		native.Source.Protocol, native.Source.Direction, native.Source.Scope = compiled.identity, direction, scope
	}
}

func (compiled *Compiled) stampEventProvenance(event *Event, direction Direction, scope Scope) error {
	compiled.stampNative(event.Unmapped, direction, scope)
	if event.Request != nil {
		event.Request.SchemaVersion, event.Request.Source = SemanticSchemaVersion, compiled.identity
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
	}
	if event.Response != nil {
		event.Response.SchemaVersion, event.Response.Source = SemanticSchemaVersion, compiled.identity
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
	defer func() { err = compiled.runtimeError(direction, err) }()
	if err := IssuesError(CheckEvent(event, compiled.target(direction, options), compiled.limits)); err != nil {
		return Value{}, err
	}
	input, err := EncodeValue(event)
	if err != nil {
		return Value{}, err
	}
	output, issues := compiled.Execute(ctx, direction, input, options)
	if err := IssuesError(issues); err != nil {
		return Value{}, err
	}
	return compiled.preserveMappedNative(ctx, direction, event.Native, output, options)
}

func (compiled *Compiled) runtimeError(direction Direction, err error) error {
	if err == nil {
		return nil
	}
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
		return &ConversionError{cause: err, Issues: issues}
	}
	code := InvalidInput
	if direction == DecodeResponse || direction == DecodeEvent {
		code = UpstreamContractViolation
	}
	return &ConversionError{cause: err, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "runtime", Path: "/", Reason: err.Error(), Suggestion: "Correct the wire input or semantic mapping and verify the revision again."}}}
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
	return stampNodeScopes(node.Children, scope)
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
	return Target{Protocol: compiled.identity, Direction: direction, Scope: options.Scope, Capabilities: compiled.mappings[direction].capabilities}
}

func (compiled *Compiled) preserveMappedNative(ctx context.Context, direction Direction, native *Native, output Value, options EvaluationContext) (Value, error) {
	// Reconciliation replays a decoder with an already pinned binding. It must
	// never repeat authorization/routing or choose a new source account.
	options.ResolveRequestScope = nil
	target := compiled.target(direction, options)
	if !compiled.native.Preserve || native == nil || !CanPreserveNative(native.Source, target) {
		return output, nil
	}
	original, issues := PreserveNative(*native, target, nil, compiled.limits)
	if err := IssuesError(issues); err != nil {
		return Value{}, err
	}
	var baseline any
	switch direction {
	case EncodeRequest:
		request, err := compiled.DecodeRequest(ctx, original.Bytes(), options)
		if err != nil {
			return Value{}, err
		}
		baseline = request
	case EncodeResponse:
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
		baseline = events[0]
	default:
		return Value{}, fmt.Errorf("native reconciliation requires a request or response direction")
	}
	value, err := EncodeValue(baseline)
	if err != nil {
		return Value{}, err
	}
	before, issues := compiled.Execute(ctx, direction, value, options)
	if err := IssuesError(issues); err != nil {
		return Value{}, err
	}
	result, err := reconcileNative(ctx, original, before, output, compiled.native, compiled.limits)
	if err != nil {
		return Value{}, IssuesError([]ConversionIssue{{Code: InvalidMutation, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "preserve", Path: native.Source.Path, Reason: err.Error(), Suggestion: "Declare stable array identities or apply an explicit native replacement."}})
	}
	return result, nil
}
