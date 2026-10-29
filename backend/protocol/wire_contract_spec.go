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
	Schema     *ValueSchema    `json:"schema"`
	Assertions []WireAssertion `json:"assertions,omitempty"`
}

// WireAssertion checks one wire location when its optional condition is true.
// An empty path selects the document; a missing selection fails validation.
// When observes the containing document, Check observes the selected value,
// and root always observes the original document. Each checks array members in
// source order, keeping diagnostic paths tied to the wire rather than output
// positions. Optional fields use an explicit exists condition.
type WireAssertion struct {
	Path   string          `json:"path"`
	When   *Expression     `json:"when,omitempty"`
	Schema ValueSchema     `json:"schema"`
	Code   IssueCode       `json:"code"`
	Reason string          `json:"reason"`
	Check  *Expression     `json:"check,omitempty"`
	Each   []WireAssertion `json:"each,omitempty"`
}

type compiledWireAssertion struct {
	path    string
	pointer []string
	when    *compiledOutputPlan
	schema  *wireSchemaPlan
	code    IssueCode
	reason  string
	check   *compiledOutputPlan
	each    []compiledWireAssertion
}

type specWireValidator struct {
	schema          *wireSchemaPlan
	assertions      []compiledWireAssertion
	limits          Limits
	rootObjectReads int
}

func compileWireContractSpec(spec WireContractSpec, path string, limits Limits) (compiledWireContract, error) {
	if spec.Schema == nil {
		return compiledWireContract{}, fmt.Errorf("%s/schema: wire schema is required", path)
	}
	schema, err := compileWireSchemaPlan(spec.Schema, limits)
	if err != nil {
		return compiledWireContract{}, locateMappingError(path+"/schema", err)
	}
	validator := &specWireValidator{schema: schema, limits: limits}
	expressions := &expressionCompiler{limits: limits}
	validator.assertions, err = compileWireAssertions(spec.Assertions, path+"/assertions", spec.Schema, spec.Schema, expressions, 1)
	if err != nil {
		return compiledWireContract{}, err
	}
	validator.rootObjectReads = countAssertionRootReads(validator.assertions)
	return compiledWireContract{validator: validator, contextValidator: validator}, nil
}

func countAssertionRootReads(assertions []compiledWireAssertion) int {
	reads := 0
	for _, assertion := range assertions {
		if len(assertion.pointer) != 0 {
			reads++
		}
		for _, predicate := range []*compiledOutputPlan{assertion.when, assertion.check} {
			if predicate != nil {
				reads += int(predicate.rootObjectReads)
			}
		}
		reads += countAssertionRootReads(assertion.each)
		if reads >= rootObjectReuseReads {
			return rootObjectReuseReads
		}
	}
	return reads
}

func compileWireAssertions(assertions []WireAssertion, path string, input, root *ValueSchema, expressions *expressionCompiler, depth int) ([]compiledWireAssertion, error) {
	if len(assertions) == 0 {
		return nil, nil
	}
	if depth > expressions.limits.Depth || len(assertions) > expressions.limits.Nodes-expressions.nodes {
		return nil, fmt.Errorf("%s: exceeds assertion depth or node limit", path)
	}
	expressions.nodes += len(assertions)
	checks := make([]compiledWireAssertion, 0, len(assertions))
	for index, assertion := range assertions {
		at := fmt.Sprintf("%s/%d", path, index)
		pointer, err := parsePointer(assertion.Path)
		if err != nil {
			return nil, locateMappingError(at+"/path", err)
		}
		if !slices.Contains([]IssueCode{InvalidInput, UnsupportedCapability, UpstreamContractViolation, InvalidAssociation, ConversionRejected}, assertion.Code) {
			return nil, fmt.Errorf("%s/code: expected an input, capability, association or wire-contract diagnostic", at)
		}
		if strings.TrimSpace(assertion.Reason) == "" {
			return nil, fmt.Errorf("%s/reason: static diagnostic text is required", at)
		}
		check := compiledWireAssertion{path: assertion.Path, pointer: pointer, code: assertion.Code, reason: assertion.Reason}
		check.when, err = compileWirePredicate(assertion.When, at+"/when", input, root, expressions, depth)
		if err != nil {
			return nil, err
		}
		check.schema, err = compileWireSchemaPlan(&assertion.Schema, expressions.limits)
		if err != nil {
			return nil, locateMappingError(at+"/schema", err)
		}
		check.check, err = compileWirePredicate(assertion.Check, at+"/check", &assertion.Schema, root, expressions, depth)
		if err != nil {
			return nil, err
		}
		if len(assertion.Each) != 0 && (assertion.Schema.Type != ArrayType || assertion.Schema.Nullable) {
			return nil, fmt.Errorf("%s/each: requires a non-nullable array schema", at)
		}
		check.each, err = compileWireAssertions(assertion.Each, at+"/each", assertion.Schema.Items, root, expressions, depth+1)
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func compileWirePredicate(expression *Expression, path string, input, root *ValueSchema, compiler *expressionCompiler, depth int) (*compiledOutputPlan, error) {
	if expression == nil {
		return nil, nil
	}
	plan, err := compiler.compilePlan(*expression, path, expressionScope{input: input, root: root}, depth)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s: condition must produce a boolean", path)
	}
	return plan, nil
}

func (validator *specWireValidator) ValidateWireOutput(direction Direction, value Value) error {
	return validator.validate(context.Background(), direction, value, validator.limits)
}

func (validator *specWireValidator) validate(ctx context.Context, direction Direction, value Value, effectiveLimits Limits) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validator.schema.validate(ctx, value, "", effectiveLimits); err != nil {
		code := InvalidInput
		if direction == DecodeResponse || direction == DecodeEvent {
			code = UpstreamContractViolation
		} else if direction == EncodeRequest || direction == EncodeResponse || direction == EncodeEvent {
			code = ConversionRejected
		}
		return wireAssertionError(code, mappingErrorPath("", err), "wire document does not match its declared schema", err)
	}
	reader := newNativeComparisonReader(value)
	state := evaluation{ctx: ctx, input: value, root: value, valueReader: reader, objectReader: newPlanObjectReader(value, validator.rootObjectReads), budget: &evaluationBudget{limits: effectiveLimits, stepLimit: effectiveLimits.Nodes}}
	return validateWireAssertions(validator.assertions, state, "")
}

func validateWireAssertions(assertions []compiledWireAssertion, state evaluation, parentPath string) error {
	for _, assertion := range assertions {
		ctx := state.ctx
		if err := state.advance(); err != nil {
			return err
		}
		path := parentPath + assertion.path
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
			err = assertion.schema.validate(ctx, selected, path, state.budget.limits)
		}
		if err != nil {
			return wireAssertionError(assertion.code, path, assertion.reason, err)
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
				return wireAssertionError(assertion.code, path, assertion.reason, err)
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
				if err := validateWireAssertions(assertion.each, child, path+"/"+strconv.Itoa(index)); err != nil {
					return err
				}
			}
		}
	}
	return state.ctx.Err()
}

func wireAssertionError(code IssueCode, path, reason string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Stage: "wire", Path: path, Reason: reason}}}
}
