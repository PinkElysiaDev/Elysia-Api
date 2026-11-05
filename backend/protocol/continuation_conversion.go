package protocol

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// ProtocolRecoveryDisposition declares the target representation of protected
// provider resources. It cannot manufacture signatures or authentication facts.
type ProtocolRecoveryDisposition struct {
	Kinds         []string    `json:"kinds"`
	PreserveWhen  *Expression `json:"preserveWhen,omitempty"`
	OnUnavailable string      `json:"onUnavailable"`
}

type continuationRecoveryOptions struct {
	Target      Identity                    `json:"target"`
	MappingHash string                      `json:"mappingHash"`
	Policy      ProtocolRecoveryDisposition `json:"policy"`
	Expressions map[string]Expression       `json:"expressions,omitempty"`
	preserve    *compiledOutputPlan
}

func (compiled *Compiled) compileRecoveryDefault(declaration ProtocolConversionDefault, compiler *expressionCompiler) (ConversionRule, error) {
	if declaration.Steps != nil || declaration.Control != nil || declaration.Auxiliary != nil || declaration.Collection || declaration.FrameUsage {
		return ConversionRule{}, fmt.Errorf("recovery must be an independent target rule")
	}
	direction := controlDirection(declaration.Phase, false)
	if declaration.Phase != ConversionRequest && declaration.Phase != ConversionResponse && declaration.Phase != ConversionEvent || !compiled.Supports(direction) {
		return ConversionRule{}, fmt.Errorf("recovery requires an implemented request or response direction")
	}
	options := continuationRecoveryOptions{Target: compiled.identity, MappingHash: compiled.mappings[direction].definitionHash, Policy: *declaration.Recovery}
	references := map[string]Expression{}
	previous := compiler.mappingReferences
	compiler.mappingReferences = references
	plan, err := compileRecoveryPredicate(options.Policy, compiler, "/mapping/conversionDefaults/"+declaration.ID+"/recovery")
	compiler.mappingReferences = previous
	if err != nil {
		return ConversionRule{}, err
	}
	options.preserve, options.Expressions = plan, references
	raw, err := EncodeValue(options)
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "continuation_recovery", Value: raw}
	fingerprint, err := conversionRuleFingerprint(rule)
	if err != nil {
		return ConversionRule{}, err
	}
	options.Policy.PreserveWhen, options.Expressions = nil, nil
	rule.preparedOption = &preparedConversionOption{phase: rule.Phase, limits: compiler.limits, declarationHash: fingerprint, option: conversionOption{raw: raw, value: options}}
	return rule, nil
}

func compileRecoveryPredicate(policy ProtocolRecoveryDisposition, compiler *expressionCompiler, path string) (*compiledOutputPlan, error) {
	if len(policy.Kinds) == 0 || len(policy.Kinds) > 2 || policy.OnUnavailable != "degrade" && policy.OnUnavailable != "reject" {
		return nil, fmt.Errorf("%s: recovery requires protected kinds and explicit unavailable behavior", path)
	}
	for i, kind := range policy.Kinds {
		if kind != "signature" && kind != "encrypted_content" || slices.Contains(policy.Kinds[:i], kind) {
			return nil, fmt.Errorf("%s/kinds: only distinct signature and encrypted_content are recoverable", path)
		}
	}
	return compileWirePredicateInScope(policy.PreserveWhen, path+"/preserveWhen", expressionScope{coreInput: reflect.TypeFor[Node]()}, compiler, 1)
}

func compileContinuationRecovery(rule ConversionRule, limits Limits) (conversionOption, error) {
	if rule.Path != "" || rule.Expression != nil || rule.Match.Path != "" || rule.Match.Present != nil || rule.Match.NodeKind != "" {
		return conversionOption{}, fmt.Errorf("recovery rules accept route matches only")
	}
	if prepared := rule.preparedOption; prepared != nil && prepared.phase == rule.Phase && prepared.limits == limits && prepared.option.raw == rule.Value {
		fingerprint, err := conversionRuleFingerprint(rule)
		if err != nil {
			return conversionOption{}, err
		}
		if fingerprint == prepared.declarationHash {
			return prepared.option, nil
		}
	}
	var options continuationRecoveryOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if options.Target.DefinitionID == "" || options.MappingHash == "" {
		return conversionOption{}, fmt.Errorf("recovery requires a pinned target mapping")
	}
	compiler := newExpressionCompiler(limits, options.Expressions)
	var err error
	options.preserve, err = compileRecoveryPredicate(options.Policy, compiler, "/rules/"+rule.ID+"/recovery")
	if err != nil {
		return conversionOption{}, err
	}
	compiler.releaseGraph()
	options.Policy.PreserveWhen, options.Expressions = nil, nil
	return conversionOption{raw: rule.Value, value: options}, nil
}

// HasContinuationRecovery reports effective enabled recovery declarations without
// scanning authored business identifiers or probing resource contents.
func (conversion *CompiledConversion) HasContinuationRecovery(phase ConversionPhase, route ConversionContext) bool {
	if conversion == nil {
		return false
	}
	for _, rule := range conversion.recovery[phase] {
		if rule.Match.matchesRoute(route) {
			return true
		}
	}
	return false
}

func (options continuationRecoveryOptions) preserves(ctx context.Context, node *Node, route ConversionContext, limits Limits) (bool, error) {
	if options.Target != route.Target {
		return false, operationMetadataError("/continuation", "recovery target differs from the pinned mapping")
	}
	if options.preserve == nil {
		return false, nil
	}
	contextValue, err := EncodeValue(route)
	if err != nil {
		return false, err
	}
	read := func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer, nil) }
	state := evaluation{ctx: ctx, inputReader: read, rootReader: read, context: contextValue, valueReader: &valueReader{}, budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}}
	value, err := options.preserve.evaluate(state)
	if err != nil {
		return false, err
	}
	preserve, err := readBoolean(value)
	if err != nil || !preserve {
		return false, err
	}
	source := route.Source
	if node.Source != nil {
		source = node.Source.Protocol
	}
	return source.Family == route.Target.Family && source.WireVersion == route.Target.WireVersion, nil
}

// NeedsContinuationRecovery selects actual protected resources before sealing.
func (conversion *CompiledConversion) NeedsContinuationRecovery(ctx context.Context, phase ConversionPhase, node Node, route ConversionContext) (bool, error) {
	if conversion == nil {
		return false, nil
	}
	for _, rule := range conversion.recovery[phase] {
		if !rule.Match.matchesRoute(route) {
			continue
		}
		options := conversion.options[rule.ID].value.(continuationRecoveryOptions)
		if !options.hasResources(&node) {
			continue
		}
		preserve, err := options.preserves(ctx, &node, route, conversion.limits)
		if err != nil {
			return false, err
		}
		if preserve {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (options continuationRecoveryOptions) hasResources(node *Node) bool {
	for _, resource := range node.Resources {
		if slices.Contains(options.Policy.Kinds, resource.Kind) {
			return true
		}
	}
	return false
}

func (conversion *CompiledConversion) applyContinuationRecovery(ctx context.Context, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	options := conversion.options[rule.ID].value.(continuationRecoveryOptions)
	if binding := route.continuation; binding != nil {
		if binding.source != route.Source || binding.compiled.identity != route.Target || binding.scope != route.Scope || binding.compiled.mappings[binding.direction].definitionHash != options.MappingHash {
			return operationMetadataError("/continuation", "recovery evidence differs from the current source, target, scope or mapping")
		}
	}
	var visit func([]Node, string) error
	visit = func(nodes []Node, path string) error {
		for i := range nodes {
			node := &nodes[i]
			at := fmt.Sprintf("%s/%d", path, i)
			preserve := true
			if options.hasResources(node) {
				var err error
				preserve, err = options.preserves(ctx, node, route, conversion.limits)
				if err != nil {
					return err
				}
			}
			if !preserve {
				kept := make([]Resource, 0, len(node.Resources))
				digest := ""
				for j, resource := range node.Resources {
					if !slices.Contains(options.Policy.Kinds, resource.Kind) {
						kept = append(kept, resource)
						continue
					}
					if digest == "" {
						if observation := route.continuationEvent; observation != nil && at == "/item/0" {
							entity := observation.entity
							if entity.binding != route.continuation || entity.isChanged || !sameContinuationEvent(observation.original, *document.(*Event)) {
								return operationMetadataError("/continuation", "conversion changed content bound to authenticated continuation")
							}
							entity.isRecovering = true
							digest = entity.digest
						} else {
							digest = ContinuationNodeDigest(*node)
						}
					}
					var sealed *SealedContinuation
					binding := route.continuation
					if binding != nil {
						sealed = binding.resources[digest+":"+ContinuationResourceKey(resource)]
					}
					hasClient := sealed != nil && binding.plan != nil
					hasRecovery := sealed != nil && (sealed.persisted || hasClient)
					if route.requireContinuation && rule.Phase != ConversionRequest && !hasClient {
						return conversion.recoveryIssue(rule, route, "/continuation", sink, false, true, "required continuation state has no client delivery plan")
					}
					if err := conversion.recoveryIssue(rule, route, fmt.Sprintf("%s/resources/%d", at, j), sink, hasRecovery, options.Policy.OnUnavailable == "reject" && !hasRecovery, "protected provider state requires the declared recovery channel"); err != nil {
						return err
					}
				}
				node.Resources = kept
			}
			if err := visit(node.Children, at+"/children"); err != nil {
				return err
			}
			if err := visit(node.ReasoningContent, at+"/reasoningContent"); err != nil {
				return err
			}
		}
		return nil
	}
	switch value := document.(type) {
	case *Request:
		return visit(value.Content, "/content")
	case *Response:
		return visit(value.Content, "/content")
	case *Event:
		if value.Item != nil {
			items := []Node{*value.Item}
			if err := visit(items, "/item"); err != nil {
				return err
			}
			*value.Item = items[0]
		}
		if value.Response != nil {
			return visit(value.Response.Content, "/response/content")
		}
	}
	return nil
}

func (conversion *CompiledConversion) recoveryIssue(rule ConversionRule, route ConversionContext, path string, sink *DiagnosticSink, recoverable, reject bool, reason string) error {
	issue := ConversionIssue{Code: ConversionDegraded, Severity: SeverityWarning, Fidelity: "lossy_compatible", Protocol: route.Target, Direction: controlDirection(rule.Phase, false), Stage: "conversion." + string(rule.Phase), Path: path, Reason: reason, RuleID: rule.ID, PolicyHash: conversion.Hash, PolicyRevision: conversion.RuleRevisions[rule.ID], Evidence: conversion.Origins[rule.ID]}
	if recoverable {
		issue.Fidelity = "recoverable_wrapped"
		issue.Reason = "protected provider state retained in authenticated continuation"
	}
	if reject || !recoverable && conversion.Policy.Mode == "strict" {
		issue.Code, issue.Severity, issue.Fidelity = ConversionRejected, SeverityError, "rejected"
		return IssuesError([]ConversionIssue{issue})
	}
	sink.Add(issue)
	return nil
}
