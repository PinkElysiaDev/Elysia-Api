package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// WireContractSpec validates a wire document independently of its mapping.
// Assertions run in authored order after the document schema succeeds.
type WireContractSpec struct {
	Base       string              `json:"base,omitempty"`
	Schema     *ValueSchema        `json:"schema,omitempty"`
	Assertions []WireAssertion     `json:"assertions,omitempty"`
	Stream     *ProtocolStreamSpec `json:"stream,omitempty"`
}

// WireAssertion checks one wire location when its optional condition is true.
// An empty path selects the document; a declared schema rejects missing values.
// When observes the containing document, Check observes the selected value,
// and root always observes the original document. Each checks array members in
// source order, keeping diagnostic paths tied to the wire rather than output
// positions. Optional fields use an explicit exists condition. Schema may be
// omitted for a check-only assertion; each always requires an array schema.
type WireAssertion struct {
	// Ref calls a named Spec constraint at Path, preserving the document root.
	// A reference cannot override individual checks in the shared declaration.
	Ref    string          `json:"ref,omitempty"`
	Path   string          `json:"path,omitempty"`
	When   *Expression     `json:"when,omitempty"`
	Schema *ValueSchema    `json:"schema,omitempty"`
	Code   IssueCode       `json:"code,omitempty"`
	Reason string          `json:"reason,omitempty"`
	Check  *Expression     `json:"check,omitempty"`
	Each   []WireAssertion `json:"each,omitempty"`
	// ReasonExpression replaces Reason and reads the same current container and
	// root as When. It runs only after this assertion fails, under its budget.
	ReasonExpression *Expression `json:"reasonExpression,omitempty"`
	// Diagnostic preserves the declared boundary's classification. A Check
	// branch using reject supplies its own diagnostic instead. A non-nil
	// diagnostic may explicitly retain an unspecified stage from that boundary.
	Diagnostic *RejectDiagnostic `json:"diagnostic,omitempty"`
}

type compiledWireAssertion struct {
	group          []compiledWireAssertion
	path           string
	pointer        []string
	when           *compiledOutputPlan
	validateSchema func(context.Context, Value, string, Limits, *inputView) error
	code           IssueCode
	reason         string
	reasonPlan     *compiledOutputPlan
	check          *compiledOutputPlan
	each           []compiledWireAssertion
	diagnostic     *RejectDiagnostic
}

type specWireValidator struct {
	schema     *wireSchemaPlan
	assertions []compiledWireAssertion
	limits     Limits
}

func (expressions *expressionCompiler) compileWireContractSpec(spec WireContractSpec, path string) (compiledWireContract, error) {
	limits := expressions.limits
	if spec.Schema == nil {
		return compiledWireContract{}, fmt.Errorf("%s/schema: wire schema is required", path)
	}
	schema, err := compileWireSchemaPlan(spec.Schema, limits)
	if err != nil {
		return compiledWireContract{}, locateMappingError(path+"/schema", err)
	}
	validator := &specWireValidator{schema: schema, limits: limits}
	validator.assertions, err = compileWireAssertions(spec.Assertions, path+"/assertions", spec.Schema, spec.Schema, expressions, 1)
	if err != nil {
		return compiledWireContract{}, err
	}
	stream, err := expressions.compilePackageStreamContract(spec.Stream, path+"/stream")
	if err != nil {
		return compiledWireContract{}, err
	}
	return compiledWireContract{validator: validator, contextValidator: validator, packageStream: stream}, nil
}

func compileWireAssertions(assertions []WireAssertion, path string, input, root *ValueSchema, expressions *expressionCompiler, depth int) ([]compiledWireAssertion, error) {
	return compileWireAssertionsInScope(assertions, path, expressionScope{input: input, root: root, isWireContract: true}, expressions, depth)
}

func compileWireAssertionsInScope(assertions []WireAssertion, path string, scope expressionScope, expressions *expressionCompiler, depth int) ([]compiledWireAssertion, error) {
	if len(assertions) == 0 {
		return nil, nil
	}
	if depth > expressions.limits.Depth || len(assertions) > expressions.limits.Nodes-expressions.nodes {
		return nil, fmt.Errorf("%s: exceeds assertion depth or node limit", path)
	}
	expressions.nodes += len(assertions)
	expressions.maximumDepth = max(expressions.maximumDepth, depth)
	checks := make([]compiledWireAssertion, 0, len(assertions))
	for index, assertion := range assertions {
		at := fmt.Sprintf("%s/%d", path, index)
		pointer, err := parsePointer(assertion.Path)
		if err != nil {
			return nil, locateMappingError(at+"/path", err)
		}
		if assertion.Ref != "" {
			selected := scope
			selected.input, err = schemaAtPointer(scope.input, pointer)
			if err != nil {
				return nil, locateMappingError(at+"/path", err)
			}
			group, err := expressions.compileWireConstraint(assertion, at, selected, depth)
			if err != nil {
				return nil, err
			}
			checks = append(checks, compiledWireAssertion{group: group, path: assertion.Path, pointer: pointer})
			continue
		}
		if assertion.Code != "" && !slices.Contains([]IssueCode{InvalidInput, UnsupportedCapability, UpstreamContractViolation, InvalidAssociation, ConversionRejected}, assertion.Code) {
			return nil, fmt.Errorf("%s/code: expected an input, capability, association or wire-contract diagnostic", at)
		}
		check := compiledWireAssertion{path: assertion.Path, pointer: pointer, code: assertion.Code, reason: assertion.Reason}
		check.reasonPlan, err = compileWireReasonInScope(assertion, at, scope, expressions, depth)
		if err != nil {
			return nil, err
		}
		if assertion.Diagnostic != nil {
			if err := validateBoundaryDiagnostic(*assertion.Diagnostic, at+"/diagnostic"); err != nil {
				return nil, err
			}
			classification := *assertion.Diagnostic
			check.diagnostic = &classification
		}
		check.when, err = compileWirePredicateInScope(assertion.When, at+"/when", scope, expressions, depth)
		if err != nil {
			return nil, err
		}
		selected := scope
		if assertion.Schema == nil {
			if assertion.Check == nil || len(assertion.Each) != 0 {
				return nil, fmt.Errorf("%s: an assertion without schema requires check and cannot use each", at)
			}
			selected.input, err = schemaAtPointer(scope.input, pointer)
			if err != nil {
				return nil, locateMappingError(at+"/path", err)
			}
			check.validateSchema = acceptWireCheckSelection
		} else {
			schema, schemaErr := compileWireSchemaPlan(assertion.Schema, expressions.limits)
			if schemaErr != nil {
				return nil, locateMappingError(at+"/schema", schemaErr)
			}
			check.validateSchema = schema.validateWithin
			selected.input = assertion.Schema
		}
		check.check, err = compileWirePredicateInScope(assertion.Check, at+"/check", selected, expressions, depth)
		if err != nil {
			return nil, err
		}
		if len(assertion.Each) != 0 {
			if assertion.Schema.Type != ArrayType || assertion.Schema.Nullable {
				return nil, fmt.Errorf("%s/each: requires a non-nullable array schema", at)
			}
			selected.input = assertion.Schema.Items
			check.each, err = compileWireAssertionsInScope(assertion.Each, at+"/each", selected, expressions, depth+1)
			if err != nil {
				return nil, err
			}
		}
		checks = append(checks, check)
	}
	return checks, nil
}

// A check-only assertion observes the selected value without adding a second
// shape traversal. Its predicate determines how to handle an absent selection.
func acceptWireCheckSelection(context.Context, Value, string, Limits, *inputView) error {
	return nil
}

func compileWirePredicateInScope(expression *Expression, path string, scope expressionScope, compiler *expressionCompiler, depth int) (*compiledOutputPlan, error) {
	if expression == nil {
		return nil, nil
	}
	plan, err := compiler.compilePlan(*expression, path, scope, depth)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s: condition must produce a boolean", path)
	}
	return plan, nil
}

func compileWireReasonInScope(assertion WireAssertion, path string, scope expressionScope, compiler *expressionCompiler, depth int) (*compiledOutputPlan, error) {
	if assertion.ReasonExpression == nil {
		if strings.TrimSpace(assertion.Reason) == "" {
			return nil, fmt.Errorf("%s/reason: diagnostic text or reasonExpression is required", path)
		}
		return nil, nil
	}
	if assertion.Reason != "" {
		return nil, fmt.Errorf("%s/reasonExpression: cannot be combined with reason", path)
	}
	path += "/reasonExpression"
	plan, err := compiler.compilePlan(*assertion.ReasonExpression, path, scope, depth)
	if err != nil {
		return nil, err
	}
	if plan.expression.result != StringType || !localProjectionCondition(plan) {
		return nil, fmt.Errorf("%s: diagnostic must produce a string using only the current wire input or root", path)
	}
	return plan, nil
}

func (validator *specWireValidator) ValidateWireOutput(direction Direction, value Value) error {
	return validator.validate(context.Background(), direction, value, validator.limits)
}

func (validator *specWireValidator) validate(ctx context.Context, direction Direction, value Value, effectiveLimits Limits) error {
	return validator.validateInputView(ctx, direction, newInputView(value), effectiveLimits)
}

func (validator *specWireValidator) validateInputView(ctx context.Context, direction Direction, view *inputView, effectiveLimits Limits) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if view.validator == validator && view.direction == direction && view.specLimits == effectiveLimits {
		return nil
	}
	value := view.value
	if err := validator.schema.validateWithin(ctx, value, "", effectiveLimits, view); err != nil {
		return wireAssertionError(wireBoundaryCode(direction), mappingErrorPath("", err), "wire document does not match its declared schema", err)
	}
	state := evaluation{ctx: ctx, input: value, root: value, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), budget: &evaluationBudget{limits: effectiveLimits, stepLimit: effectiveLimits.Nodes}}
	if err := validateWireAssertions(validator.assertions, state, "", direction); err != nil {
		return err
	}
	view.validator, view.direction, view.specLimits = validator, direction, effectiveLimits
	return nil
}

func validateWireAssertions(assertions []compiledWireAssertion, state evaluation, parentPath string, direction Direction) error {
	for _, assertion := range assertions {
		path := parentPath + assertion.path
		if assertion.group != nil {
			selected := state
			var err error
			selected.input, err = readValuePointerWithObjectReader(state.input, assertion.pointer, state.valueReader, state.objectReader)
			if err != nil {
				return wireAssertionError(wireBoundaryCode(direction), path, "constraint cannot select its declared input", err)
			}
			if err := validateWireAssertions(assertion.group, selected, path, direction); err != nil {
				return err
			}
			continue
		}
		ctx := state.ctx
		if err := state.advance(); err != nil {
			return err
		}
		if assertion.when != nil {
			condition, err := assertion.when.evaluate(state)
			if err != nil {
				return err
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return wireAssertionError(InvalidInput, path, "wire assertion condition must produce a boolean", err)
			}
			if !isApplicable {
				continue
			}
		}
		selected, err := readValuePointerWithObjectReader(state.input, assertion.pointer, state.valueReader, state.objectReader)
		if err == nil {
			err = assertion.validateSchema(ctx, selected, path, state.budget.limits, state.inputView)
		}
		if err != nil {
			return assertion.failure(state, path, err, direction)
		}
		if assertion.check != nil {
			selection := state
			selection.input = selected
			value, err := assertion.check.evaluate(selection)
			isValid := false
			if err == nil {
				isValid, err = readBoolean(value)
			}
			if err != nil || !isValid {
				var checked *declaredCheckError
				if errors.As(err, &checked) {
					return err
				}
				var rejection *declaredRejectionError
				if errors.As(err, &rejection) {
					return &ConversionError{cause: err, Issues: []ConversionIssue{rejection.issue(Identity{}, "")}}
				}
				return assertion.failure(state, path, err, direction)
			}
		}
		if len(assertion.each) != 0 {
			items, err := selected.readArrayWithin(state.valueReader)
			if err != nil {
				return err
			}
			for index, item := range items {
				child := state
				child.input = item
				if err := validateWireAssertions(assertion.each, child, path+"/"+strconv.Itoa(index), direction); err != nil {
					return err
				}
			}
		}
	}
	return state.ctx.Err()
}

func wireAssertionError(code IssueCode, path, reason string, cause error) error {
	return classifiedWireAssertionError(code, "", "wire", path, reason, "", cause)
}

func (assertion compiledWireAssertion) failure(state evaluation, path string, cause error, direction Direction) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	reason := assertion.reason
	if assertion.reasonPlan != nil {
		value, err := assertion.reasonPlan.evaluate(state)
		if err == nil {
			reason, err = readString(value)
		}
		if err != nil {
			return errors.Join(cause, err)
		}
	}
	if diagnostic := assertion.diagnostic; diagnostic != nil {
		if diagnostic.Path != "" {
			path = diagnostic.Path
		}
		return classifiedWireAssertionError(diagnostic.Code, diagnostic.Direction, diagnostic.Stage, path, reason, diagnostic.Suggestion, cause)
	}
	code := assertion.code
	if code == "" {
		code = wireBoundaryCode(direction)
	}
	return wireAssertionError(code, path, reason, cause)
}

func wireBoundaryCode(direction Direction) IssueCode {
	switch direction {
	case DecodeResponse, DecodeEvent:
		return UpstreamContractViolation
	case EncodeRequest, EncodeResponse, EncodeEvent, EncodeUpstreamEvent:
		return ConversionRejected
	default:
		return InvalidInput
	}
}

func classifiedWireAssertionError(code IssueCode, direction Direction, stage, path, reason, suggestion string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: code, Direction: direction, Severity: SeverityError, Stage: stage, Path: path, Reason: reason, Suggestion: suggestion}}}
}
