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
	if phase == ConversionSourceEvent {
		return Value{}, operationMetadataError("/phase", "source_event requires a source normalizer bound to its decoder")
	}
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
	if phase == ConversionEvent && route.frameUsage != nil {
		facts := route.frameUsage
		if facts.source != route.Source || facts.target != route.Target || facts.scope != route.Scope {
			return Value{}, operationMetadataError("/frame/usage", "frame usage belongs to a different route or scope")
		}
		if err := state.load(phase, true); err != nil {
			return Value{}, err
		}
		var err error
		// Select before target rules can replace the response's presentation ID.
		route.selectedFrameUsage, err = facts.forEvent(*state.document.(*Event))
		if err != nil {
			return Value{}, err
		}
	}
	value := input
	var modelControls *modelControlExecution
	for _, rule := range c.phases[phase] {
		activeRule = rule
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		var err error
		if state.accepts(phase, rule) {
			err = state.apply(ctx, c, phase, rule, route, sink)
			if err != nil {
				return Value{}, err
			}
			if state.resolvedModelControls != nil {
				modelControls, state.resolvedModelControls = state.resolvedModelControls, nil
			}
			continue
		}
		value, err = state.flush()
		if err != nil {
			return Value{}, err
		}
		associations := captureRangeAssociations(state.document)
		switch {
		case rule.Match.NodeKind != "":
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
		*state = conversionState{wire: value, limits: limits, auxiliary: state.auxiliary, omittedAuxiliary: state.omittedAuxiliary, control: state.control, omittedControl: state.omittedControl, rangeAssociations: associations}
		if len(associations) != 0 {
			if err := state.load(phase, true); err != nil {
				return Value{}, err
			}
		}
		if phase == ConversionEvent && (state.auxiliary != nil || state.omittedAuxiliary != nil || state.control != nil || state.omittedControl != nil) {
			if err := state.load(phase, true); err != nil {
				return Value{}, err
			}
		}
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
	if modelControls != nil {
		if err := state.load(ConversionRequest, false); err != nil {
			return Value{}, err
		}
		if err := modelControls.apply(ctx, c, state.document.(*Request), route, sink, modelControls.plan.checks, false); err != nil {
			return Value{}, candidateFailure(err)
		}
		if err := modelControls.validateFinalCore(state.document.(*Request)); err != nil {
			return Value{}, candidateFailure(err)
		}
		if request := state.document.(*Request); request.Reasoning != nil {
			if err := modelControls.apply(ctx, c, request, route, sink, modelControls.plan.renderChecks, false); err != nil {
				return Value{}, candidateFailure(err)
			}
		}
	}
	return value, ctx.Err()
}

// JSON-dependent matches and authored operations retain the original boundary.
// Contiguous built-in actions use applyRequestObject on an owned document.

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
		if err == nil && value != input {
			err = c.issue(rule, phase, route, rule.Path, "explicit field conversion: "+rule.Action, sink, true)
		}
	case "transform":
		contextValue, _ := EncodeValue(route)
		value, err = c.expressions[rule.ID].evaluate(evaluation{ctx: ctx, input: value, root: value, context: contextValue, budget: &evaluationBudget{limits: c.ruleLimits(ctx, phase, route), stepLimit: c.limits.Nodes}})
		if err == nil && value != input {
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
			isBuiltin := rule.Action == "provider_signature"
			isMatch := false
			if rule.Match.NodeKind == "" || rule.Match.NodeKind == n.Kind {
				if isBuiltin {
					isMatch = rule.Match.matchesSemantic(route, n)
				} else {
					var err error
					v, err = EncodeValue(n)
					if err != nil {
						return err
					}
					isMatch = rule.Match.matches(route, v)
				}
			}
			if isMatch {
				if rule.Action == "provider_signature" && !HasSignature(*n) {
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
				} else if !isBuiltin {
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
	// This checks changes in protected ownership; validity and scope have their
	// own boundaries. Identical bytes cannot introduce or rebind protected data.
	if before == after {
		return nil
	}
	allowed := map[string]int{}
	var collect func(Value, bool, *valueReader) error
	collect = func(v Value, checking bool, reader *valueReader) error {
		if v.IsObject() {
			fields, err := v.readObjectWithin(reader)
			if err != nil {
				return err
			}
			for key, child := range fields {
				if key == "source" || key == "native" || key == "scope" || key == "resources" || key == "thoughtSignature" || key == "signature" || key == "encrypted_content" || key == "elysia_continuation" {
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
				if err := collect(child, checking, reader); err != nil {
					return err
				}
			}
		} else if strings.HasPrefix(strings.TrimSpace(v.raw), "[") {
			items, err := v.readArrayWithin(reader)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := collect(item, checking, reader); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Only fixed-size digests escape this synchronous traversal. Keep original
	// spelling for protection checks without cloning every enclosing payload.
	if err := collect(before, false, newNativeComparisonReader(before)); err != nil {
		return err
	}
	return collect(after, true, newNativeComparisonReader(after))
}
