// Frozen e376367 execution boundaries. Keep independent of optimized dispatch.
// Business helpers not changed by this refactor remain shared; changed helpers
// below are frozen too. This oracle is compiled only into protocol tests.
package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (c *CompiledConversion) ApplyValueBaseline(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink) (output Value, resultErr error) {
	var activeRule ConversionRule
	defer func() {
		direction := DecodeRequest
		if phase == ConversionResponse {
			direction = DecodeResponse
		}
		if phase == ConversionEvent {
			direction = DecodeEvent
		}
		if phase == ConversionWire {
			direction = EncodeRequest
			if route.ResponseWire {
				direction = EncodeResponse
			}
		}
		resultErr = resourceIssue(resultErr, route.Source, direction, "conversion."+string(phase))
		var failure *ResourceLimitError
		var conversion *ConversionError
		if errors.As(resultErr, &failure) && errors.As(resultErr, &conversion) && c != nil {
			for i := range conversion.Issues {
				conversion.Issues[i].RuleID = activeRule.ID
				conversion.Issues[i].PolicyHash = c.Hash
				conversion.Issues[i].PolicyRevision = c.RuleRevisions[activeRule.ID]
				conversion.Issues[i].Evidence = c.Origins[activeRule.ID]
			}
		}
	}()
	if phase == ConversionSourceEvent {
		return Value{}, streamIssue(InvalidInput, "/sequence", "source_event requires a frame sequence")
	}
	if !slices.Contains(conversionPhases(), phase) {
		return Value{}, streamIssue(InvalidInput, "/phase", "unknown conversion phase")
	}
	if c == nil {
		return input, nil
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	limits := c.phaseLimits(ctx, phase)
	if phase == ConversionWire && !route.ResponseWire {
		limits = requestLimits(ctx, c.limits, c.Policy.Limits != nil)
	}
	if err := checkResourceValue(input, limits, "semantic"); err != nil {
		return Value{}, err
	}
	if err := validateContextValue(phase, input); err != nil {
		return Value{}, err
	}
	value := input
	var reasoningTarget string
	for _, rule := range c.Policy.Rules {
		if !rule.Enabled || rule.Phase != phase {
			continue
		}
		activeRule = rule
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		var err error
		switch {
		case rule.Action == "system_instruction_hoist" || rule.Action == "chat_history" || rule.Action == "assistant_history" || rule.Action == "stream_options" || rule.Action == "request_projection" || rule.Action == "responses_include" || rule.Action == "responses_context" || rule.Action == "responses_storage" || rule.Action == "gemini_safety_settings" || rule.Action == "reasoning_controls":
			if !rule.Match.matches(route, value) {
				continue
			}
			var resolvedTarget string
			value, resolvedTarget, err = c.applyRequestActionBaseline(ctx, value, route, rule, sink)
			if resolvedTarget != "" {
				reasoningTarget = resolvedTarget
			}
		case rule.Action == "chat_response_model" || rule.Action == "anthropic_usage_envelope" || rule.Action == "response_shape" || rule.Action == "response_envelope" || rule.Action == "response_metadata" || rule.Action == "error_metadata" || rule.Action == "usage_projection" || rule.Action == "responses_turn_state":
			if rule.Match.matches(route, value) {
				value, err = c.applyResponseActionBaseline(ctx, phase, value, route, rule, sink)
			}
		case rule.Action == "signatures" || rule.Match.NodeKind != "":
			value, err = c.applyNodeRuleBaseline(ctx, phase, value, route, sink, rule)
		default:
			if rule.Match.matches(route, value) {
				value, err = c.applyRule(ctx, phase, value, route, sink, rule)
			}
		}
		if err != nil {
			return Value{}, err
		}
		if err = checkResourceValue(value, limits, "semantic"); err != nil {
			return Value{}, err
		}
	}
	if err := validateContextValue(phase, value); err != nil {
		return Value{}, candidateFailure(err)
	}
	// Later authored rules must not invalidate the resolved target controls.
	if reasoningTarget != "" {
		var req Request
		if err := value.Decode(&req); err != nil {
			return Value{}, err
		}
		cap := ResolveReasoningCapabilities(reasoningTarget, route.Model)
		if route.Reasoning != nil {
			cap = *route.Reasoning
		}
		if req.Reasoning != nil && req.Reasoning.Interleaved && !route.InterleavedThinking {
			return Value{}, candidateFailure(streamIssue(InvalidInput, "/reasoning/interleaved", "target operation lacks the interleaved thinking beta"))
		}
		if err := validateReasoningOutputLimit(&req, cap, reasoningTarget); err != nil {
			return Value{}, candidateFailure(err)
		}
		if err := validateReasoningModel(&req, reasoningTarget, req.Reasoning, cap); err != nil {
			return Value{}, candidateFailure(err)
		}
		if _, err := ReasoningParameters(&req, reasoningTarget); err != nil {
			return Value{}, candidateFailure(err)
		}
	}
	return value, ctx.Err()
}

func (c *CompiledConversion) applyRequestActionBaseline(ctx context.Context, value Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, string, error) {
	var request Request
	if err := decodeContract(value.Bytes(), &request); err != nil {
		return Value{}, "", err
	}
	var err error
	var reasoningTarget string
	switch rule.Action {
	case "system_instruction_hoist":
		err = c.hoistSystem(&request, route, rule, sink)
	case "chat_history", "assistant_history":
		err = c.assistantHistoryBaseline(&request, route, rule, sink)
	case "reasoning_controls":
		err = c.reasoningControls(&request, route, rule, sink)
		if err == nil {
			var options reasoningOptions
			err = rule.Value.Decode(&options)
			reasoningTarget = options.TargetCodec
		}
	case "responses_storage":
		err = c.responsesStorage(&request, route, rule, sink)
	case "responses_include":
		err = c.responsesInclude(&request, route, rule, sink)
	case "gemini_safety_settings":
		err = c.geminiSafetySettings(&request, route, rule, sink)
	case "request_projection":
		err = c.requestProjection(&request, route, rule, sink)
	case "responses_context":
		err = c.responsesContext(&request, route, rule, sink)
	case "stream_options":
		err = c.streamOptions(&request, route, rule, sink)
	}
	if err != nil {
		return Value{}, "", err
	}
	value, err = encodeSemanticWithin(&request, c.phaseLimits(ctx, ConversionRequest))
	return value, reasoningTarget, err
}

func (c *CompiledConversion) applyResponseActionBaseline(ctx context.Context, phase ConversionPhase, value Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	switch rule.Action {
	case "chat_response_model":
		var response Response
		if err := value.Decode(&response); err != nil {
			return Value{}, err
		}
		if err := c.repairChatResponseModel(&response, route, rule, sink); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&response, c.phaseLimits(ctx, phase))
	case "responses_turn_state":
		return c.projectResponsesTurnStateBaseline(value, rule, sink, c.phaseLimits(ctx, phase))
	case "anthropic_usage_envelope":
		return c.envelopeUsageValueBaseline(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "response_shape", "response_envelope":
		return c.responseValueBaseline(rule.Action, phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "response_metadata":
		return c.metadataValueBaseline(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "error_metadata":
		return c.errorMetadataValueBaseline(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "usage_projection":
		return c.projectUsageValueBaseline(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	}
	return Value{}, fmt.Errorf("unsupported response conversion action %q", rule.Action)
}

func (c *CompiledConversion) applyNodeRuleBaseline(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, rule ConversionRule) (Value, error) {
	var visit func([]Node, string) error
	visit = func(nodes []Node, base string) error {
		for i := range nodes {
			n := &nodes[i]
			at := fmt.Sprintf("%s/%d", base, i)
			v, err := encodeSemanticWithin(n, c.phaseLimits(ctx, phase))
			if err != nil {
				return err
			}
			if (rule.Match.NodeKind == "" || rule.Match.NodeKind == n.Kind) && rule.Match.matches(route, v) {
				if rule.Action == "signatures" {
					foreign := route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion
					restored := n.Source != nil && n.Source.Protocol.Family == route.Target.Family && n.Source.Protocol.WireVersion == route.Target.WireVersion
					if foreign && !restored {
						kept := make([]Resource, 0, len(n.Resources))
						for j, res := range n.Resources {
							if !IsContinuationResource(res) {
								kept = append(kept, res)
								continue
							}
							recoverable := route.Recoverable[ContinuationNodeDigest(*n)] || route.Recoverable[ContinuationResourceKey(res)]
							reason := "foreign " + res.Kind + " has no target representation; recovery unavailable"
							if recoverable {
								reason = "foreign " + res.Kind + " preserved in authenticated continuation state"
							}
							if err := c.issue(rule, phase, route, fmt.Sprintf("%s/resources/%d", at, j), reason, sink, !recoverable); err != nil {
								return err
							}
						}
						n.Resources = kept
					}
				} else if rule.Action == "provider_signature" && !HasSignature(*n) {
					if !c.VerifiedProviderRules[rule.ID] {
						return streamIssue(VerificationRequired, at+"/resources", "provider signature compatibility value requires verification for this policy, protocol revision, account and model")
					}
					signature, err := ProviderSignatureValue(rule.Value)
					if err != nil {
						return err
					}
					if err := c.issue(rule, phase, route, at+"/resources", "upstream-verified compatibility signature substituted for missing original state", sink, true); err != nil {
						return err
					}
					n.Resources = append(n.Resources, Resource{Kind: "signature", ID: signature, Scope: route.Scope})
					n.Source = &Provenance{Protocol: route.Target, Direction: DecodeResponse, Scope: route.Scope}
					n.Native = nil
				} else if rule.Action == "tool_result_text" || rule.Action == "tool_result_object" {
					if err := c.toolResultBaseline(n, route, rule, at, sink); err != nil {
						return err
					}
				} else {
					out, err := c.applyRule(ctx, phase, v, route, sink, rule)
					if err != nil {
						return err
					}
					var changed Node
					if err := decodeContract(out.Bytes(), &changed); err != nil {
						return err
					}
					*n = changed
				}
			}
			if err := visit(n.Children, at+"/children"); err != nil {
				return err
			}
			if err := visit(n.ReasoningContent, at+"/reasoningContent"); err != nil {
				return err
			}
		}
		return nil
	}
	switch phase {
	case ConversionIngress, ConversionRequest:
		var r Request
		if err := decodeContract(input.Bytes(), &r); err != nil {
			return Value{}, err
		}
		if err := visit(r.Content, "/content"); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&r, c.phaseLimits(ctx, phase))
	case ConversionResponse:
		var r Response
		if err := decodeContract(input.Bytes(), &r); err != nil {
			return Value{}, err
		}
		if err := visit(r.Content, "/content"); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&r, c.phaseLimits(ctx, phase))
	case ConversionEvent:
		var e Event
		if err := decodeContract(input.Bytes(), &e); err != nil {
			return Value{}, err
		}
		if e.Item != nil {
			nodes := []Node{*e.Item}
			if err := visit(nodes, "/item"); err != nil {
				return Value{}, err
			}
			e.Item = &nodes[0]
		}
		if e.Response != nil {
			if err := visit(e.Response.Content, "/response/content"); err != nil {
				return Value{}, err
			}
		}
		return encodeSemanticWithin(&e, c.phaseLimits(ctx, phase))
	}
	return Value{}, fmt.Errorf("node rules require a semantic phase")
}

func (c *CompiledConversion) RequestBaseline(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	if c != nil {
		if err := checkSemanticValue(request, c.phaseLimits(ctx, ConversionRequest)); err != nil {
			return nil, resourceIssue(err, route.Source, DecodeRequest, "conversion.request")
		}
	}
	v, err := EncodeSemantic(ctx, DecodeRequest, request)
	if err != nil {
		return nil, err
	}
	v, err = c.ApplyValueBaseline(ctx, ConversionIngress, v, route, sink)
	if err == nil {
		v, err = c.ApplyValueBaseline(ctx, ConversionRequest, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Request
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	if c.Policy.Usage != nil {
		if result.ClientOutput == nil {
			result.ClientOutput = &ClientOutput{}
		}
		result.ClientOutput.CollectUsage = c.Policy.Usage.CollectUpstreamUsage
	}
	return &result, nil
}

func (c *CompiledConversion) ResponseBaseline(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	v, err := EncodeSemantic(ctx, DecodeResponse, response)
	if err == nil {
		v, err = c.ApplyValueBaseline(ctx, ConversionResponse, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Response
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *CompiledConversion) EventBaseline(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (Event, error) {
	v, err := EncodeSemantic(ctx, DecodeEvent, event)
	if err == nil {
		v, err = c.ApplyValueBaseline(ctx, ConversionEvent, v, route, sink)
	}
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = decodeContract(v.Bytes(), &result)
	return result, err
}

func (c *CompiledConversion) metadataValueBaseline(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	var codec string
	_ = rule.Value.Decode(&codec)
	var visit func(*Node) error
	visit = func(n *Node) error {
		var err error
		n.Metadata, err = c.projectMetadata(n.Metadata, codec, phase, route, rule, sink)
		if err != nil {
			return err
		}
		for i := range n.Children {
			if err = visit(&n.Children[i]); err != nil {
				return err
			}
		}
		for i := range n.ReasoningContent {
			if err = visit(&n.ReasoningContent[i]); err != nil {
				return err
			}
		}
		if codec == "responses" && n.Kind == MessageNode {
			var keep []ResponseMetadata
			for _, m := range n.Metadata {
				if m.Location != "content" {
					keep = append(keep, m)
					continue
				}
				textIndex := -1
				for i := range n.Children {
					if n.Children[i].Kind == TextNode {
						if textIndex != -1 {
							return streamIssue(UnsupportedCapability, m.Path, "message metadata requires unambiguous text ownership")
						}
						textIndex = i
					}
				}
				if textIndex == -1 {
					return streamIssue(UnsupportedCapability, m.Path, "content metadata has no text owner")
				}
				n.Children[textIndex].Metadata = append(n.Children[textIndex].Metadata, m)
			}
			n.Metadata = keep
		}
		return nil
	}
	response := func(r *Response) error {
		if r == nil {
			return nil
		}
		if err := ValidateResponseOutputText(r); err != nil {
			return err
		}
		var err error
		r.Metadata, err = c.projectMetadata(r.Metadata, codec, phase, route, rule, sink)
		if err != nil {
			return err
		}
		for i := range r.Content {
			if err = visit(&r.Content[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if phase == ConversionRequest {
		var r Request
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		for i := range r.Content {
			if err := visit(&r.Content[i]); err != nil {
				return Value{}, err
			}
		}
		return encodeSemanticWithin(&r, limits)
	}
	if phase == ConversionResponse {
		var r Response
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		if err := response(&r); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&r, limits)
	}
	var e Event
	if err := input.Decode(&e); err != nil {
		return Value{}, err
	}
	var err error
	e.Metadata, err = c.projectMetadata(e.Metadata, codec, phase, route, rule, sink)
	if err != nil {
		return Value{}, err
	}
	if err = response(e.Response); err != nil {
		return Value{}, err
	}
	if e.Item != nil {
		if err = visit(e.Item); err != nil {
			return Value{}, err
		}
	}
	return encodeSemanticWithin(&e, limits)
}

func (c *CompiledConversion) errorMetadataValueBaseline(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	if phase == ConversionResponse {
		var response Response
		if err := input.Decode(&response); err != nil {
			return Value{}, err
		}
		var err error
		response.Error, err = c.projectErrorMetadata(response.Error, route, rule, sink)
		if err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&response, limits)
	}
	var event Event
	if err := input.Decode(&event); err != nil {
		return Value{}, err
	}
	var err error
	event.Error, err = c.projectErrorMetadata(event.Error, route, rule, sink)
	if err != nil {
		return Value{}, err
	}
	if event.Response != nil {
		event.Response.Error, err = c.projectErrorMetadata(event.Response.Error, route, rule, sink)
		if err != nil {
			return Value{}, err
		}
	}
	return encodeSemanticWithin(&event, limits)
}

func (c *CompiledConversion) projectUsageValueBaseline(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	if phase == ConversionResponse {
		var response Response
		if err := input.Decode(&response); err != nil {
			return Value{}, err
		}
		if err := c.projectUsage(response.Usage, phase, route, rule, sink, "/usage"); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&response, limits)
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
	return encodeSemanticWithin(&event, limits)
}

func (c *CompiledConversion) envelopeUsageValueBaseline(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
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
		return encodeSemanticWithin(&response, limits)
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
	return encodeSemanticWithin(&event, limits)
}

func (c *CompiledConversion) responseValueBaseline(action string, phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	var codec string
	_ = rule.Value.Decode(&codec)
	apply := func(r *Response) error {
		if r == nil {
			return nil
		}
		if !r.Error.IsZero() && !r.Error.IsNull() {
			return nil
		}
		if action == "response_shape" {
			return c.responseShape(r, phase, route, rule, sink)
		}
		prepareDelivery(r, route, codec)
		return nil
	}
	if phase == ConversionResponse {
		var r Response
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		if err := apply(&r); err != nil {
			return Value{}, err
		}
		return encodeSemanticWithin(&r, limits)
	}
	var e Event
	if err := input.Decode(&e); err != nil {
		return Value{}, err
	}
	if action == "response_shape" && codec != "responses" {
		if !e.ParentID.IsZero() {
			if err := c.issue(rule, phase, route, "/parentId", "target expresses message content as a flat assistant turn", sink, true); err != nil {
				return Value{}, err
			}
			e.ParentID = Value{}
		}
		if e.Item != nil && e.Item.Kind == MessageNode {
			n := e.Item
			if len(n.Children) > 0 || len(n.Attributes) > 0 || len(n.Metadata) > 0 || len(n.Cache) > 0 || len(n.Resources) > 0 || !n.Payload.IsZero() || (!n.Role.IsString("assistant") && !n.Role.IsZero()) {
				return Value{}, streamIssue(UnsupportedCapability, "/item", "message container has data requiring an explicit projection")
			}
			if err := c.issue(rule, phase, route, "/item", "target cannot retain this message container identity, status and boundary", sink, true); err != nil {
				return Value{}, err
			}
			e.Type, e.Item, e.ItemID, e.Index = MetadataUpdated, nil, Value{}, nil
		}
	}
	if action == "response_shape" && codec == "openai-chat" {
		if err := c.chatEventShape(&e, route, rule, sink); err != nil {
			return Value{}, err
		}
	}
	if action == "response_shape" && e.Item != nil {
		r := Response{Content: []Node{*e.Item}}
		if err := c.responseShape(&r, phase, route, rule, sink); err != nil {
			return Value{}, err
		}
		if len(r.Content) != 1 {
			return Value{}, streamIssue(UnsupportedCapability, "/item", "stream item projection must retain one node association")
		}
		e.Item = &r.Content[0]
	}
	if e.Type == ResponseStarted || e.Type == ResponseFinished {
		if e.Response == nil {
			e.Response = &Response{SchemaVersion: SemanticSchemaVersion, ID: e.ResponseID}
		}
		if err := apply(e.Response); err != nil {
			return Value{}, err
		}
		e.ResponseID = e.Response.ID
	}
	return encodeSemanticWithin(&e, limits)
}

func (c *CompiledConversion) projectResponsesTurnStateBaseline(value Value, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	var event Event
	if err := decodeContract(value.Bytes(), &event); err != nil {
		return Value{}, err
	}
	state := event.ResponsesTurnState
	if state == nil {
		return value, nil
	}
	if err := ValidateResponsesTurnStateFrame(state.Frame); err != nil {
		return Value{}, err
	}
	if rule.Value.IsString("responses") {
		return value, nil
	}
	issue := ConversionIssue{
		Code: ConversionDegraded, Severity: SeverityWarning, Fidelity: "lossy_compatible",
		Protocol: state.Source.Protocol, Direction: DecodeEvent, Stage: "conversion.event", Path: ResponsesTurnStatePath,
		RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID],
		Reason: fmt.Sprintf("provider frame %d: target has no turn-state control channel; omitted state does not preserve stateful continuation", state.FrameNumber),
	}
	if c.Policy.Mode == "strict" {
		issue.Code, issue.Severity, issue.Fidelity = ConversionRejected, SeverityError, "rejected"
		return Value{}, IssuesError([]ConversionIssue{issue})
	}
	sink.Add(issue)
	event.ResponsesTurnState, event.Native = nil, nil
	return encodeSemanticWithin(&event, limits)
}

func (c *CompiledConversion) toolResultBaseline(n *Node, route ConversionContext, rule ConversionRule, at string, sink *DiagnosticSink) error {
	failed, err := toolResultError(n.Status, at+"/status")
	if err != nil {
		return err
	}
	if n.Children == nil && (rule.Action == "tool_result_object" || rule.Value.IsString("openai-chat") || rule.Value.IsString("responses")) && !n.Payload.IsZero() && !n.Payload.IsNull() {
		if blocks, err := n.Payload.readArray(); err == nil && len(blocks) == 0 {
			n.Payload = StringValue("")
			c.normalized(rule, route, at+"/payload", "empty tool result blocks normalized to empty text", sink)
		}
	}
	if rule.Action == "tool_result_text" {
		if n.Payload.IsObject() {
			if err := c.issue(rule, ConversionRequest, route, at+"/payload", "object tool result serialized as JSON text; payload type changes", sink, true); err != nil {
				return err
			}
			n.Payload = StringValue(string(n.Payload.Bytes()))
		}
		var codec string
		_ = rule.Value.Decode(&codec)
		if n.Status.IsZero() || (codec != "openai-chat" && codec != "responses") {
			return nil
		}
		if failed {
			if err := c.issue(rule, ConversionRequest, route, at+"/status", "tool error flag represented by a text marker; target has no separate error flag", sink, true); err != nil {
				return err
			}
			const marker = "[Tool error] "
			if n.Children != nil {
				n.Children = append([]Node{{Kind: TextNode, Payload: StringValue(marker)}}, n.Children...)
			} else {
				var text string
				if (!n.Payload.IsZero() && n.Payload.Decode(&text) != nil) || n.Payload.IsNull() {
					return streamIssue(InvalidInput, at+"/payload", "tool result requires text or content blocks")
				}
				n.Payload = StringValue(marker + text)
			}
		} else {
			c.normalized(rule, route, at+"/status", "false tool error flag normalized to an ordinary result", sink)
		}
		n.Status = Value{}
		return nil
	}

	if n.Children != nil {
		var text strings.Builder
		for i, block := range n.Children {
			path := fmt.Sprintf("%s/children/%d", at, i)
			if block.Kind != TextNode || block.ReasoningForm != "" || block.ReasoningContent != nil || !block.Role.IsZero() || !block.ID.IsZero() || !block.CallID.IsZero() || !block.Name.IsZero() || !block.Status.IsZero() || block.Input != nil || len(block.Children) > 0 || len(block.Cache) > 0 || len(block.Resources) > 0 || len(block.Attributes) > 0 || len(block.Metadata) > 0 {
				return candidateFailure(streamIssue(UnsupportedCapability, path, "object tool results require plain text blocks; media and scoped state need explicit mapping"))
			}
			var value string
			if block.Payload.IsNull() || block.Payload.Decode(&value) != nil {
				return streamIssue(InvalidInput, path+"/payload", "text payload must be a string")
			}
			text.WriteString(value)
		}
		if err := c.issue(rule, ConversionRequest, route, at+"/children", "tool result text blocks combined into object content; block boundaries change", sink, true); err != nil {
			return err
		}
		n.Payload, n.Children = StringValue(text.String()), nil
	}
	if !n.Payload.IsObject() {
		var text string
		if (!n.Payload.IsZero() && n.Payload.Decode(&text) != nil) || n.Payload.IsNull() {
			return streamIssue(InvalidInput, at+"/payload", "tool_result_object requires text or an object")
		}
		key := "result"
		if !rule.Value.IsZero() && (rule.Value.Decode(&key) != nil || key == "") {
			return fmt.Errorf("tool_result_object requires a nonempty field name")
		}
		if err := c.issue(rule, ConversionRequest, route, at+"/payload", "text tool result wrapped in the configured object field", sink, true); err != nil {
			return err
		}
		n.Payload, _ = EncodeValue(Object{key: StringValue(text)})
	}
	if !n.Status.IsZero() {
		if failed {
			n.Payload, _ = EncodeValue(Object{"error": n.Payload})
		} else if fields, _ := n.Payload.ReadObject(); !fields["error"].IsZero() || !fields["output"].IsZero() {
			// Reserved response keys in caller data still belong to successful output.
			n.Payload, _ = EncodeValue(Object{"output": n.Payload})
		}
		c.normalized(rule, route, at+"/status", "tool result error state mapped to object response semantics", sink)
		n.Status = Value{}
	}
	return nil
}

func (c *CompiledConversion) assistantHistoryBaseline(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var project func([]Node, string) error
	project = func(nodes []Node, at string) error {
		for i := range nodes {
			n := &nodes[i]
			path := fmt.Sprintf("%s/%d", at, i)
			if n.Kind == MessageNode || n.Kind == ToolCallNode || n.Kind == ToolResultNode || n.Kind == ReasoningNode {
				status := n.Status
				// Tool errors are semantic state, not history item bookkeeping.
				if n.Kind == ToolResultNode && status.IsObject() {
					status = Value{}
				}
				if !n.ID.IsZero() || !status.IsZero() {
					var id, state string
					if (!n.ID.IsZero() && !n.ID.IsNull() && n.ID.Decode(&id) != nil) || (!status.IsZero() && !status.IsNull() && (status.Decode(&state) != nil || (state != "completed" && state != "in_progress" && state != "incomplete"))) {
						return candidateFailure(streamIssue(UnsupportedCapability, path, "unknown history identity/status requires explicit mapping"))
					}
					if err := c.issue(rule, ConversionRequest, route, path, "target cannot express history item identity/status; tool call IDs and error state are retained", sink, true); err != nil {
						return err
					}
					n.ID = Value{}
					if !status.IsZero() {
						n.Status = Value{}
					}
					n.Native, request.Native = nil, nil
				}
			}
			if err := project(n.Children, path+"/children"); err != nil {
				return err
			}
			if err := project(n.ReasoningContent, path+"/reasoningContent"); err != nil {
				return err
			}
		}
		return nil
	}
	if err := project(request.Content, "/content"); err != nil {
		return err
	}

	chat := rule.Action == "chat_history"
	if chat {
		var content []Node
		for i, n := range request.Content {
			results := 0
			for _, child := range n.Children {
				if child.Kind == ToolResultNode {
					results++
				}
			}
			if n.Kind != MessageNode || results == 0 || results == len(n.Children) {
				content = append(content, n)
				continue
			}
			if len(n.Cache) > 0 || len(n.Resources) > 0 || len(n.Attributes) > 0 || len(n.Metadata) > 0 {
				return candidateFailure(streamIssue(UnsupportedCapability, fmt.Sprintf("/content/%d", i), "mixed tool-result message metadata requires explicit mapping"))
			}
			if err := c.issue(rule, ConversionRequest, route, fmt.Sprintf("/content/%d", i), "Chat splits tool results and surrounding content into ordered messages", sink, true); err != nil {
				return err
			}
			for _, child := range n.Children {
				if child.Kind == ToolResultNode {
					content = append(content, child)
				} else {
					part := n
					part.Children, part.Native = []Node{child}, nil
					content = append(content, part)
				}
			}
			request.Native = nil
		}
		request.Content = content
	}
	var result, pending []Node
	start, bare := 0, false
	changed := false
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		defer func() { pending, bare = nil, false }()
		if !bare {
			if !chat {
				result = append(result, pending...)
				return nil
			}
			// Native Chat message boundaries remain intact. Other codecs can
			// still place text between calls inside one assistant message.
			for i, n := range pending {
				children, reordered, err := c.chatHistoryChildren(n.Children, fmt.Sprintf("/content/%d/children", start+i), route, rule, sink)
				if err != nil {
					return err
				}
				if reordered {
					n.Children, n.Native = children, nil
					changed = true
				}
				result = append(result, n)
			}
			return nil
		}
		at := fmt.Sprintf("/content/%d", start)
		n := Node{Kind: MessageNode, Role: StringValue("assistant")}
		messages := 0
		for i, item := range pending {
			path := fmt.Sprintf("/content/%d", start+i)
			if item.Kind == MessageNode {
				messages++
				if len(item.Cache) > 0 || len(item.Resources) > 0 || len(item.Attributes) > 0 || len(item.Metadata) > 0 || !item.Payload.IsZero() || !item.Name.IsZero() || !item.CallID.IsZero() || item.Input != nil {
					return candidateFailure(streamIssue(UnsupportedCapability, path, "assistant item grouping cannot move message metadata across thinking, text or tools"))
				}
			}
			if item.Kind == MessageNode {
				n.Children = append(n.Children, item.Children...)
			} else {
				n.Children = append(n.Children, item)
			}
		}
		if messages > 1 {
			if err := c.issue(rule, ConversionRequest, route, at, "target combines message boundaries within this assistant turn; thinking and parallel tool calls stay in the same message", sink, true); err != nil {
				return err
			}
		}
		if chat {
			children, _, err := c.chatHistoryChildren(n.Children, at+"/children", route, rule, sink)
			if err != nil {
				return err
			}
			n.Children = children
		}
		hasReply := false
		for _, child := range n.Children {
			hasReply = hasReply || child.Kind == TextNode || child.Kind == ToolCallNode || child.Kind == RefusalNode
		}
		if chat && !hasReply {
			return candidateFailure(streamIssue(UnsupportedCapability, at, "Chat thinking history requires associated assistant content or tool calls before the next turn"))
		}
		result = append(result, n)
		changed = true
		c.normalized(rule, route, at, "thinking, text and tool output items attached to their assistant history turn", sink)
		return nil
	}
	for i, node := range request.Content {
		if node.Kind == ReasoningNode || node.Kind == ToolCallNode || (node.Kind == MessageNode && node.Role.IsString("assistant")) {
			if len(pending) == 0 {
				start = i
			}
			bare = bare || node.Kind != MessageNode
			pending = append(pending, node)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		result = append(result, node)
	}
	if err := flush(); err != nil {
		return err
	}
	if changed {
		request.Content, request.Native = result, nil
	}
	return nil
}
