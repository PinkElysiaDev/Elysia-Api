package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
// Optional fields use an explicit exists condition. Reason is static text.
type WireAssertion struct {
	Path   string      `json:"path"`
	When   *Expression `json:"when,omitempty"`
	Schema ValueSchema `json:"schema"`
	Code   IssueCode   `json:"code"`
	Reason string      `json:"reason"`
}

type compiledWireAssertion struct {
	path    string
	pointer []string
	when    *compiledExpression
	schema  *wireSchemaPlan
	code    IssueCode
	reason  string
}

type specWireValidator struct {
	schema     *wireSchemaPlan
	assertions []compiledWireAssertion
	limits     Limits
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
	if len(spec.Assertions) > limits.Nodes {
		return compiledWireContract{}, fmt.Errorf("%s/assertions: exceeds node limit", path)
	}
	expressions := &expressionCompiler{limits: limits}
	for index, assertion := range spec.Assertions {
		at := fmt.Sprintf("%s/assertions/%d", path, index)
		pointer, err := parsePointer(assertion.Path)
		if err != nil {
			return compiledWireContract{}, locateMappingError(at+"/path", err)
		}
		if !slices.Contains([]IssueCode{InvalidInput, UnsupportedCapability, UpstreamContractViolation, InvalidAssociation, ConversionRejected}, assertion.Code) {
			return compiledWireContract{}, fmt.Errorf("%s/code: expected an input, capability, association or wire-contract diagnostic", at)
		}
		if strings.TrimSpace(assertion.Reason) == "" {
			return compiledWireContract{}, fmt.Errorf("%s/reason: static diagnostic text is required", at)
		}
		check := compiledWireAssertion{path: assertion.Path, pointer: pointer, code: assertion.Code, reason: assertion.Reason}
		if assertion.When != nil {
			check.when, err = expressions.compile(*assertion.When, at+"/when", expressionScope{input: spec.Schema}, 1)
			if err != nil {
				return compiledWireContract{}, err
			}
			if !isAssignable(check.when.result, BooleanType) {
				return compiledWireContract{}, fmt.Errorf("%s/when: condition must produce a boolean", at)
			}
		}
		check.schema, err = compileWireSchemaPlan(&assertion.Schema, limits)
		if err != nil {
			return compiledWireContract{}, locateMappingError(at+"/schema", err)
		}
		validator.assertions = append(validator.assertions, check)
	}
	return compiledWireContract{validator: validator, contextValidator: validator}, nil
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
	reader := requestValueReader(value)
	state := evaluation{ctx: ctx, input: value, root: value, valueReader: reader, budget: &evaluationBudget{limits: effectiveLimits, stepLimit: effectiveLimits.Nodes}}
	for _, assertion := range validator.assertions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if assertion.when != nil {
			condition, err := assertion.when.evaluate(state)
			if err != nil {
				return err
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return wireAssertionError(InvalidInput, assertion.path, "wire assertion condition must produce a boolean", err)
			}
			if !isApplicable {
				continue
			}
		}
		selected, err := readValuePointerWithin(value, assertion.pointer, reader)
		if err == nil {
			err = assertion.schema.validate(ctx, selected, assertion.path, effectiveLimits)
		}
		if err != nil {
			return wireAssertionError(assertion.code, assertion.path, assertion.reason, err)
		}
	}
	return ctx.Err()
}

func wireAssertionError(code IssueCode, path, reason string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Stage: "wire", Path: path, Reason: reason}}}
}
