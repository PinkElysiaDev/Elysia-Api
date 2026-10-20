package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ApplyValue executes bounded semantic or wire rules. Protected state cannot be
// minted through expressions; the separately authenticated continuation module
// is the only restoration authority.
func (c *CompiledConversion) ApplyValue(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink) (Value, error) {
	return c.applyConversionState(ctx, phase, input, route, sink, nil, true)
}

func (c *CompiledConversion) applyConversionState(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, state *conversionState, returnValue bool) (output Value, resultErr error) {
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
	if state == nil {
		state = &conversionState{wire: input, limits: limits}
	}
	state.limits = limits
	if state.document == nil {
		if err := checkResourceValue(input, limits, "semantic"); err != nil {
			return Value{}, err
		}
	} else if err := state.cache.check(state.document, limits); err != nil {
		return Value{}, err
	}
	if err := state.validateContext(phase); err != nil {
		return Value{}, err
	}
	value := input
	var reasoningTarget string
	for _, rule := range c.phases[phase] {
		activeRule = rule
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		var err error
		if state.accepts(phase, rule) {
			var target string
			target, err = state.apply(ctx, c, phase, rule, route, sink)
			if err != nil {
				return Value{}, err
			}
			if target != "" {
				reasoningTarget = target
			}
			continue
		}
		value, err = state.flush()
		if err != nil {
			return Value{}, err
		}
		switch {
		case rule.Action == "system_instruction_hoist" || rule.Action == "chat_history" || rule.Action == "assistant_history" || rule.Action == "stream_options" || rule.Action == "request_projection" || rule.Action == "responses_include" || rule.Action == "responses_context" || rule.Action == "responses_storage" || rule.Action == "gemini_safety_settings" || rule.Action == "reasoning_controls":
			if !rule.Match.matches(route, value) {
				continue
			}
			var resolvedTarget string
			value, resolvedTarget, err = c.applyRequestAction(ctx, value, route, rule, sink)
			if resolvedTarget != "" {
				reasoningTarget = resolvedTarget
			}
		case rule.Action == "chat_response_model" || rule.Action == "anthropic_usage_envelope" || rule.Action == "response_shape" || rule.Action == "response_envelope" || rule.Action == "response_metadata" || rule.Action == "error_metadata" || rule.Action == "usage_projection" || rule.Action == "responses_turn_state":
			if rule.Match.matches(route, value) {
				value, err = c.applyResponseAction(ctx, phase, value, route, rule, sink)
			}
		case rule.Action == "signatures" || rule.Match.NodeKind != "":
			value, err = c.applyNodeRule(ctx, phase, value, route, sink, rule)
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
		*state = conversionState{wire: value, limits: limits}
	}
	if returnValue {
		var flushErr error
		value, flushErr = state.flush()
		if flushErr != nil {
			return Value{}, flushErr
		}
	}
	if err := state.validateContext(phase); err != nil {
		return Value{}, candidateFailure(err)
	}
	// Later authored rules must not invalidate the resolved target controls.
	if reasoningTarget != "" {
		if err := state.load(ConversionRequest, false); err != nil {
			return Value{}, err
		}
		req := state.document.(*Request)
		cap := ResolveReasoningCapabilities(reasoningTarget, route.Model)
		if route.Reasoning != nil {
			cap = *route.Reasoning
		}
		if req.Reasoning != nil && req.Reasoning.Interleaved && !route.InterleavedThinking {
			return Value{}, candidateFailure(streamIssue(InvalidInput, "/reasoning/interleaved", "target operation lacks the interleaved thinking beta"))
		}
		if err := validateReasoningOutputLimit(req, cap, reasoningTarget); err != nil {
			return Value{}, candidateFailure(err)
		}
		if err := validateReasoningModel(req, reasoningTarget, req.Reasoning, cap); err != nil {
			return Value{}, candidateFailure(err)
		}
		if _, err := ReasoningParameters(req, reasoningTarget); err != nil {
			return Value{}, candidateFailure(err)
		}
	}
	return value, ctx.Err()
}

// JSON-dependent matches and authored operations retain the original boundary.
// Contiguous built-in actions use applyRequestObject on an owned document.
func (c *CompiledConversion) applyRequestAction(ctx context.Context, value Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, string, error) {
	var request Request
	if err := decodeContract(value.Bytes(), &request); err != nil {
		return Value{}, "", err
	}
	target, err := c.applyRequestObject(&request, route, rule, sink)
	if err != nil {
		return Value{}, "", err
	}
	value, err = encodeSemanticWithin(&request, c.phaseLimits(ctx, ConversionRequest))
	return value, target, err
}

func (c *CompiledConversion) applyRequestObject(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (string, error) {
	var err error
	var reasoningTarget string
	switch rule.Action {
	case "system_instruction_hoist":
		err = c.hoistSystem(request, route, rule, sink)
	case "chat_history", "assistant_history":
		err = c.assistantHistory(request, route, rule, sink)
	case "reasoning_controls":
		err = c.reasoningControls(request, route, rule, sink)
		if err == nil {
			var options reasoningOptions
			options, err = readConversionOption[reasoningOptions](c, rule)
			reasoningTarget = options.TargetCodec
		}
	case "responses_storage":
		err = c.responsesStorage(request, route, rule, sink)
	case "responses_include":
		err = c.responsesInclude(request, route, rule, sink)
	case "gemini_safety_settings":
		err = c.geminiSafetySettings(request, route, rule, sink)
	case "request_projection":
		err = c.requestProjection(request, route, rule, sink)
	case "responses_context":
		err = c.responsesContext(request, route, rule, sink)
	case "stream_options":
		err = c.streamOptions(request, route, rule, sink)
	}
	if err != nil {
		return "", err
	}
	return reasoningTarget, nil
}

func (c *CompiledConversion) applyResponseAction(ctx context.Context, phase ConversionPhase, value Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
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
		return c.projectResponsesTurnState(value, rule, sink, c.phaseLimits(ctx, phase))
	case "anthropic_usage_envelope":
		return c.envelopeUsageValue(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "response_shape", "response_envelope":
		return c.responseValue(rule.Action, phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "response_metadata":
		return c.metadataValue(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "error_metadata":
		return c.errorMetadataValue(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	case "usage_projection":
		return c.projectUsageValue(phase, value, route, rule, sink, c.phaseLimits(ctx, phase))
	}
	return Value{}, fmt.Errorf("unsupported response conversion action %q", rule.Action)
}

func (c *CompiledConversion) applyRule(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, rule ConversionRule) (Value, error) {
	value := input
	var err error
	switch rule.Action {
	case "warn", "reject":
		err = c.issue(rule, phase, route, rule.Path, rule.Reason, sink, false)
	case "set", "remove":
		op := SetValue
		if rule.Action == "remove" {
			ptr, _ := parsePointer(rule.Path)
			old, e := readValuePointer(value, ptr)
			if e != nil {
				return Value{}, e
			}
			if old.IsZero() {
				return value, nil
			}
			op = DeleteValue
		}
		limits := c.phaseLimits(ctx, phase)
		if phase == ConversionWire && !route.ResponseWire {
			limits = requestLimits(ctx, c.limits, c.Policy.Limits != nil)
		}
		value, err = ApplyMutations(value, []Mutation{{Op: op, Path: rule.Path, Value: rule.Value}}, limits)
		if err == nil && hashValue(value) != hashValue(input) {
			err = c.issue(rule, phase, route, rule.Path, "explicit field conversion: "+rule.Action, sink, true)
		}
	case "transform":
		contextValue, _ := EncodeValue(route)
		value, err = c.expressions[rule.ID].evaluate(evaluation{ctx: ctx, input: value, root: value, context: contextValue, budget: &evaluationBudget{limits: c.ruleLimits(ctx, phase, route), stepLimit: c.limits.Nodes}})
		if err == nil && hashValue(value) != hashValue(input) {
			err = c.issue(rule, phase, route, rule.Path, "explicit expression conversion changed semantic or wire fields", sink, true)
		}
	}
	if err == nil {
		err = protectConversionProvenance(input, value)
	}
	return value, err
}

// Only semantic node locations are traversed; tool arguments and arbitrary user
// JSON cannot masquerade as nodes or influence the restoration authority.
func (c *CompiledConversion) applyNodeRule(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, rule ConversionRule) (Value, error) {
	var document any
	switch phase {
	case ConversionIngress, ConversionRequest:
		document = &Request{}
	case ConversionResponse:
		document = &Response{}
	case ConversionEvent:
		document = &Event{}
	default:
		return Value{}, fmt.Errorf("node rules require a semantic phase")
	}
	if err := decodeContract(input.Bytes(), document); err != nil {
		return Value{}, err
	}
	if err := c.applyNodeObject(ctx, phase, document, route, sink, rule, nil); err != nil {
		return Value{}, err
	}
	return encodeSemanticWithin(document, c.phaseLimits(ctx, phase))
}

func (c *CompiledConversion) applyNodeObject(ctx context.Context, phase ConversionPhase, document any, route ConversionContext, sink *DiagnosticSink, rule ConversionRule, cache *semanticCache) error {
	var visit func([]Node, string) error
	visit = func(nodes []Node, base string) error {
		for i := range nodes {
			n := &nodes[i]
			at := fmt.Sprintf("%s/%d", base, i)
			// Unmatched nodes still have the same resource/encoding contract, but
			// built-in actions do not need a JSON copy of every ancestor subtree.
			if err := cache.check(n, c.phaseLimits(ctx, phase)); err != nil {
				return err
			}
			var v Value
			isBuiltin := rule.Action == "signatures" || rule.Action == "provider_signature" || rule.Action == "tool_result_text" || rule.Action == "tool_result_object"
			if (rule.Match.NodeKind == "" || rule.Match.NodeKind == n.Kind) && (rule.Match.Present != nil || !isBuiltin) {
				var err error
				v, err = EncodeValue(n)
				if err != nil {
					return err
				}
			}
			if (rule.Match.NodeKind == "" || rule.Match.NodeKind == n.Kind) && rule.Match.matches(route, v) {
				if rule.Action == "signatures" {
					foreign := route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion
					restored := n.Source != nil && n.Source.Protocol.Family == route.Target.Family && n.Source.Protocol.WireVersion == route.Target.WireVersion
					if foreign && !restored {
						kept := make([]Resource, 0, len(n.Resources))
						var digest string
						hasDigest := false
						for j, res := range n.Resources {
							if !IsContinuationResource(res) {
								kept = append(kept, res)
								continue
							}
							recoverable := false
							if len(route.Recoverable) > 0 {
								recoverable = route.Recoverable[ContinuationResourceKey(res)]
								if !recoverable {
									if !hasDigest {
										digest, hasDigest = ContinuationNodeDigest(*n), true
									}
									recoverable = route.Recoverable[digest]
								}
							}
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
					if err := c.toolResult(n, route, rule, at, sink); err != nil {
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

	switch subject := document.(type) {
	case *Request:
		return visit(subject.Content, "/content")
	case *Response:
		return visit(subject.Content, "/content")
	case *Event:
		if subject.Item != nil {
			nodes := []Node{*subject.Item}
			if err := visit(nodes, "/item"); err != nil {
				return err
			}
			subject.Item = &nodes[0]
		}
		if subject.Response != nil {
			return visit(subject.Response.Content, "/response/content")
		}
		return nil
	}
	return fmt.Errorf("node rules require a semantic phase")
}

func protectConversionProvenance(before, after Value) error {
	allowed := map[string]int{}
	var collect func(Value, bool) error
	collect = func(v Value, checking bool) error {
		if v.IsObject() {
			fields, err := v.ReadObject()
			if err != nil {
				return err
			}
			for key, child := range fields {
				if key == "source" || key == "native" || key == "scope" || key == "resources" || key == "thoughtSignature" || key == "signature" || key == "encrypted_content" || key == "elysia_continuation" || key == "responsesTurnState" {
					id := key + ":" + hashValue(child)
					if key == "resources" {
						owner := Object{}
						for field, data := range fields {
							if field != "source" && field != "native" && field != "resources" && field != "children" {
								owner[field] = data
							}
						}
						identity, _ := EncodeValue(owner)
						id += ":" + hashValue(identity)
					}
					if checking && allowed[id] == 0 {
						return fmt.Errorf("conversion cannot create or alter protected %s", key)
					}
					if !checking {
						allowed[id]++
					} else {
						allowed[id]--
					}
					continue
				}
				if err := collect(child, checking); err != nil {
					return err
				}
			}
		} else if strings.HasPrefix(strings.TrimSpace(v.raw), "[") {
			items, err := v.readArray()
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := collect(item, checking); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(before, false); err != nil {
		return err
	}
	return collect(after, true)
}
