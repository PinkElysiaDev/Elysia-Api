package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

type controlStepResult struct {
	hasChanges       bool
	changedReasoning bool
	shouldStop       bool
	shouldOmit       bool
}

func compileTypedControlSetter(root reflect.Type, path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	if root == reflect.TypeFor[Request]() {
		setter, kind, err := compileControlSetter(path)
		if err != nil {
			return nil, "", err
		}
		return func(document any, output JSONOutput, reader *valueReader) error {
			return setter(document.(*Request), output, reader)
		}, kind, nil
	}
	if root == reflect.TypeFor[Response]() && path == "/model" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			return assignCoreValue(output, &document.(*Response).Model, reader)
		}, AnyType, nil
	}
	if root == reflect.TypeFor[Response]() && path == "/id" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlValue(output, &document.(*Response).ID, reader)
		}, AnyType, nil
	}
	if root == reflect.TypeFor[Response]() && path == "/usage" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlUsage(output, &document.(*Response).Usage, reader)
		}, ObjectType, nil
	}
	if root == reflect.TypeFor[Response]() {
		return compileResponseAttributeSetter(path)
	}
	if root == reflect.TypeFor[Event]() && path == "/usage" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlUsage(output, &document.(*Event).Usage, reader)
		}, ObjectType, nil
	}
	if root == reflect.TypeFor[Event]() && path == "/responseId" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlValue(output, &document.(*Event).ResponseID, reader)
		}, StringType, nil
	}
	if root == reflect.TypeFor[Event]() {
		var selectField func(*Response) *Value
		switch path {
		case "/response/id":
			selectField = func(response *Response) *Value { return &response.ID }
		case "/response/model":
			selectField = func(response *Response) *Value { return &response.Model }
		default:
			return compileEventResponseAttributeSetter(path)
		}
		return func(document any, output JSONOutput, reader *valueReader) error {
			response := document.(*Event).Response
			if response == nil {
				return &declaredRejectionError{diagnostic: RejectDiagnostic{Code: InvalidInput, Path: "/response"}, reason: "field update requires an existing response"}
			}
			return assignCoreValue(output, selectField(response), reader)
		}, StringType, nil
	}
	if root == reflect.TypeFor[ResponseMetadata]() {
		return compileMetadataControlSetter(path)
	}
	if root == reflect.TypeFor[Usage]() {
		return compileUsageControlSetter(path)
	}
	if root == reflect.TypeFor[Node]() {
		return compileNodeControlSetter(path)
	}
	if root == reflect.TypeFor[Tool]() {
		return compileToolControlSetter(path)
	}
	return nil, "", fmt.Errorf("control update does not support %s field %s", root.Name(), path)
}

func applyControlSteps(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, steps []compiledControlStep, owner *coreControlOptions) (controlStepResult, error) {
	result := controlStepResult{}
	for _, step := range steps {
		if err := state.ctx.Err(); err != nil {
			return result, err
		}
		if step.when != nil {
			condition, err := step.when.evaluate(state)
			if err != nil {
				return result, err
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return result, err
			}
			if !isApplicable {
				continue
			}
		}
		if diagnostic := step.diagnostic; diagnostic != nil {
			declaration := diagnostic.declaration
			if diagnostic.source != nil {
				location, err := diagnostic.source.evaluate(state)
				if err != nil {
					return result, err
				}
				declaration.Path, err = readString(location)
				if err != nil {
					return result, err
				}
				if _, err := parsePointer(declaration.Path); err != nil {
					return result, err
				}
			}
			message, err := diagnostic.reason.evaluate(state)
			if err != nil {
				return result, err
			}
			reason, err := readString(message)
			if err != nil {
				return result, err
			}
			if err := conversion.controlDiagnostic(rule, route, declaration, reason, sink); err != nil {
				return result, err
			}
		}
		if step.shouldOmit {
			result.shouldOmit = true
			return result, nil
		}
		if step.each != nil {
			changed, err := step.each.apply(state, conversion, document, route, rule, sink, owner)
			if err != nil {
				return result, err
			}
			result.hasChanges = result.hasChanges || changed
		}
		if step.layout != nil {
			changed, err := step.layout.apply(state, conversion, document, route, rule, sink, owner)
			if err != nil {
				return result, err
			}
			result.hasChanges = result.hasChanges || changed
		}
		if step.auxiliary != nil {
			options := auxiliaryConversionOptions{CoreVersion: owner.CoreVersion, Target: owner.Target, EncoderHash: owner.MappingHash, DecoderHash: owner.DecoderHash, PreserveNative: owner.PreserveNative, Disposition: *step.auxiliary}
			changed, err := conversion.applyAuxiliaryDisposition(document.(*Event), route, rule, sink, options)
			if err != nil {
				return result, err
			}
			result.hasChanges = result.hasChanges || changed
		}
		values := make([]JSONOutput, len(step.updates))
		for index, update := range step.updates {
			value, err := update.program.build(state)
			if err != nil {
				return result, err
			}
			values[index] = value.output
		}
		for index, update := range step.updates {
			if err := update.set(document, values[index], state.valueReader); err != nil {
				return result, err
			}
			result.hasChanges = true
			result.changedReasoning = result.changedReasoning || update.changesReasoning
		}
		if step.shouldStop {
			result.shouldStop = true
			break
		}
	}
	return result, nil
}

func (conversion *CompiledConversion) controlDiagnostic(rule ConversionRule, route ConversionContext, diagnostic ProtocolControlDiagnostic, reason string, sink *DiagnosticSink) error {
	if diagnostic.Kind == "invalid" {
		return IssuesError([]ConversionIssue{{Code: diagnostic.Code, Severity: SeverityError, Direction: diagnostic.Direction, Stage: diagnostic.Stage, Path: diagnostic.Path, Reason: reason, Suggestion: diagnostic.Suggestion}})
	}
	issue := ConversionIssue{Code: ConversionDegraded, Severity: SeverityWarning, Fidelity: "lossy_compatible",
		Protocol: route.Target, Direction: controlDirection(rule.Phase, false), Stage: "conversion." + string(rule.Phase),
		Path: diagnostic.Path, Reason: reason, RuleID: rule.ID, PolicyHash: conversion.Hash,
		PolicyRevision: conversion.RuleRevisions[rule.ID], Evidence: conversion.Origins[rule.ID]}
	if diagnostic.Attribution == "source" {
		issue.Protocol, issue.Direction = route.Source, controlDirection(rule.Phase, true)
	}
	if diagnostic.Kind == "normalize" {
		issue.Code, issue.Severity, issue.Fidelity = ConversionNormalized, SeverityInfo, "preserved"
	} else if diagnostic.Kind == "reject" || conversion.Policy.Mode == "strict" {
		issue.Code, issue.Severity, issue.Fidelity = ConversionRejected, SeverityError, "rejected"
		failure := IssuesError([]ConversionIssue{issue})
		if rule.Phase == ConversionRequest {
			return candidateFailure(failure)
		}
		return failure
	}
	sink.Add(issue)
	return nil
}

func controlDirection(phase ConversionPhase, isSource bool) Direction {
	switch phase {
	case ConversionRequest:
		if isSource {
			return DecodeRequest
		}
		return EncodeRequest
	case ConversionResponse:
		if isSource {
			return DecodeResponse
		}
		return EncodeResponse
	case ConversionEvent:
		if isSource {
			return DecodeEvent
		}
		return EncodeEvent
	default:
		return ""
	}
}

func (conversion *CompiledConversion) applyCoreControls(ctx context.Context, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	return conversion.applySelectedCoreControls(ctx, document, route, rule, sink, false)
}

func (conversion *CompiledConversion) applySelectedCoreControls(ctx context.Context, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, isCollection bool) (bool, error) {
	options, err := readConversionOption[coreControlOptions](conversion, rule)
	if err != nil {
		return false, err
	}
	if options.Phase != rule.Phase || options.Target != route.Target && !(isCollection && options.Collection && route.Target == route.Source) {
		return false, streamIssue(InvalidInput, "/conversionDefaults", "core controls do not match the pinned target and phase")
	}
	var contextValue Value
	if options.hasContext {
		contextValue, err = EncodeValue(struct {
			Transport    Transport                   `json:"transport"`
			Model        string                      `json:"model"`
			Observations map[string]fieldObservation `json:"observations,omitempty"`
		}{route.Transport, route.Model, route.fieldObservations})
		if err != nil {
			return false, err
		}
	}
	var read func([]string) (JSONOutput, error)
	switch current := document.(type) {
	case *Request:
		if current.SchemaVersion != options.CoreVersion {
			return false, streamIssue(InvalidInput, "/schemaVersion", "core controls require their declared core version")
		}
		read = func(pointer []string) (JSONOutput, error) { return readCoreRequestOutput(current, pointer, nil) }
	case *Response:
		if current.SchemaVersion != options.CoreVersion {
			return false, streamIssue(InvalidInput, "/schemaVersion", "core controls require their declared core version")
		}
		read = func(pointer []string) (JSONOutput, error) { return readCoreResponseOutput(current, pointer, nil) }
	case *Event:
		if current.SchemaVersion != options.CoreVersion {
			return false, streamIssue(InvalidInput, "/schemaVersion", "core controls require their declared core version")
		}
		read = func(pointer []string) (JSONOutput, error) { return readCoreEventOutput(current, pointer, nil) }
	default:
		return false, fmt.Errorf("core controls require their compiled core document")
	}
	limits := conversion.phaseLimits(ctx, rule.Phase)
	ownerLimits := options.Limits
	if rule.Phase == ConversionRequest {
		ownerLimits = requestLimits(ctx, ownerLimits, options.ExplicitLimits)
	}
	limits = intersectControlLimits(limits, semanticLimits(ownerLimits))
	state := evaluation{ctx: ctx, inputReader: read, rootReader: read, context: contextValue, valueReader: &valueReader{},
		objectReader: newPlanObjectReader(contextValue, objectReuseReads), bindings: make([]JSONOutput, options.bindingSlots), budget: &evaluationBudget{limits: limits, stepLimit: min(conversion.limits.Nodes, options.Limits.Nodes)},
		runtime: evaluationRuntime{isBound: true, operation: route.operation}}
	state.coreRequest, _ = document.(*Request)
	if options.FrameUsage {
		state.runtime.hasFrameUsage = route.frameUsage != nil
		state.runtime.frameUsage = route.selectedFrameUsage
	}
	result, err := applyControlSteps(state, conversion, document, route, rule, sink, options.steps, &options)
	if err != nil {
		var classified interface {
			issue(Identity, Direction) ConversionIssue
		}
		if errors.As(err, &classified) {
			issue := classified.issue(route.Target, controlDirection(rule.Phase, false))
			issue.Stage = "conversion." + string(rule.Phase)
			issue.RuleID, issue.PolicyHash, issue.PolicyRevision, issue.Evidence = rule.ID, conversion.Hash, conversion.RuleRevisions[rule.ID], conversion.Origins[rule.ID]
			err = &ConversionError{cause: err, Issues: []ConversionIssue{issue}}
			if rule.Phase == ConversionRequest && issue.Code == ConversionRejected {
				err = candidateFailure(err)
			}
		}
	}
	return result.hasChanges, err
}
