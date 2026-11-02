package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// ProtocolStreamDisposition declares a loss that a target event program may
// request. It never grants resource, provenance or delivery authority.
type ProtocolStreamDisposition struct {
	Kind   string `json:"kind" jsonschema:"enum=degrade,enum=reject"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type streamDispositionOptions struct {
	CoreVersion int                       `json:"coreVersion"`
	Target      Identity                  `json:"target"`
	EncoderHash string                    `json:"encoderHash"`
	Disposition ProtocolStreamDisposition `json:"disposition"`
}

func (compiled *Compiled) compileStreamDisposition(declaration ProtocolConversionDefault) (ConversionRule, error) {
	if declaration.Phase != ConversionEvent || declaration.Use != "" || declaration.Recovery != nil || declaration.Steps != nil || declaration.Auxiliary != nil || declaration.Control != nil || declaration.Collection || declaration.FrameUsage {
		return ConversionRule{}, fmt.Errorf("streamDisposition requires a standalone target event rule")
	}
	value, err := EncodeValue(streamDispositionOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity,
		EncoderHash: compiled.mappings[EncodeEvent].definitionHash, Disposition: *declaration.StreamDisposition})
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "stream_disposition", Value: value}
	_, err = compileStreamDispositionOption(rule)
	return rule, err
}

func compileStreamDispositionOption(rule ConversionRule) (conversionOption, error) {
	if rule.Phase != ConversionEvent || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" || rule.Match.Path != "" || rule.Match.Present != nil {
		return conversionOption{}, fmt.Errorf("stream_disposition requires a complete event declaration and route conditions only")
	}
	var options streamDispositionOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	disposition := options.Disposition
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || options.EncoderHash == "" || (disposition.Kind != "degrade" && disposition.Kind != "reject") || strings.TrimSpace(disposition.Reason) == "" {
		return conversionOption{}, fmt.Errorf("stream_disposition requires a pinned target, degrade or reject, and a reason")
	}
	if _, err := parsePointer(disposition.Path); err != nil {
		return conversionOption{}, err
	}
	return conversionOption{raw: rule.Value, value: options}, nil
}

type streamConversionBinding struct {
	conversion *CompiledConversion
	route      ConversionContext
}

// WithConversion binds the host's resolved policy and route to target event
// transactions. Only an actual disposition request requires this binding.
func (options EvaluationContext) WithConversion(conversion *CompiledConversion, route ConversionContext) EvaluationContext {
	options.conversion = &streamConversionBinding{conversion: conversion, route: route}
	return options
}

type streamDispositionRequest struct{ name, path string }

func (plan *compiledOutputPlan) requireStreamDisposition(state evaluation) (plannedOutput, error) {
	output, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	path, err := readPlannedString(output.output)
	if err != nil {
		return plannedOutput{}, err
	}
	if _, err := parsePointer(path); err != nil {
		return plannedOutput{}, err
	}
	transaction := state.streamTransaction
	request := streamDispositionRequest{name: plan.expression.literalText, path: path}
	for _, previous := range transaction.dispositions {
		if previous == request {
			return plannedLeaf(coreBoolean(true)), nil
		}
	}
	if len(transaction.dispositions) >= state.budget.limits.Mutations {
		return plannedOutput{}, resourceFailure("stream", "dispositions", state.budget.limits.Mutations, len(transaction.dispositions)+1)
	}
	transaction.dispositions = append(transaction.dispositions, request)
	return plannedLeaf(coreBoolean(true)), nil
}

// Resolve only requested rules. Ordinary event conversion never executes these
// declarations, and disabled or replaced defaults cannot authorize a request.
func (transaction *packageStreamTransaction) resolveDispositions() error {
	if transaction == nil || len(transaction.dispositions) == 0 {
		return nil
	}
	binding := transaction.conversion
	compiled := transaction.key.compiled
	for _, request := range transaction.dispositions {
		if binding == nil || binding.conversion == nil {
			return transaction.rejectDisposition(request, "stream layout requires a resolved conversion policy")
		}
		rule, exists := binding.conversion.streamDispositions[request.name]
		if !exists || !rule.Match.matchesRoute(binding.route) {
			return transaction.rejectDisposition(request, "stream disposition is disabled, replaced or does not match the route")
		}
		options := binding.conversion.options[request.name].value.(streamDispositionOptions)
		if options.Target != compiled.identity || binding.route.Target != compiled.identity || options.EncoderHash != compiled.mappings[transaction.key.direction].definitionHash {
			return transaction.rejectDisposition(request, "stream disposition does not match the pinned target mapping")
		}
	}
	conversion := binding.conversion
	var sink *DiagnosticSink
	if transaction.diagnostics != nil {
		sink = &transaction.pendingDiagnostics
	}
	sort.SliceStable(transaction.dispositions, func(i, j int) bool {
		return conversion.streamDispositions[transaction.dispositions[i].name].Order < conversion.streamDispositions[transaction.dispositions[j].name].Order
	})
	for _, request := range transaction.dispositions {
		rule := conversion.streamDispositions[request.name]
		disposition := conversion.options[request.name].value.(streamDispositionOptions).Disposition
		path := request.path
		if path == "" {
			path = disposition.Path
		}
		if err := conversion.controlDiagnostic(rule, binding.route, ProtocolControlDiagnostic{Kind: disposition.Kind, Path: path}, disposition.Reason, sink); err != nil {
			return err
		}
	}
	transaction.dispositions = nil
	return nil
}

func (transaction *packageStreamTransaction) rejectDisposition(request streamDispositionRequest, reason string) error {
	issue := ConversionIssue{Code: ConversionRejected, Severity: SeverityError, Fidelity: "rejected",
		Protocol: transaction.key.compiled.identity, Direction: transaction.key.direction, Stage: "conversion.event", Path: request.path, RuleID: request.name, Reason: reason}
	if binding := transaction.conversion; binding != nil && binding.conversion != nil {
		issue.PolicyHash = binding.conversion.Hash
		issue.PolicyRevision = binding.conversion.RuleRevisions[request.name]
		issue.Evidence = binding.conversion.Origins[request.name]
	}
	return IssuesError([]ConversionIssue{issue})
}

func (transaction *packageStreamTransaction) commitDiagnostics() {
	for _, issue := range transaction.pendingDiagnostics.issues {
		// Paths retained beyond the event must not keep its input buffer alive.
		issue.Path = strings.Clone(issue.Path)
		transaction.diagnostics.Add(issue)
	}
}
