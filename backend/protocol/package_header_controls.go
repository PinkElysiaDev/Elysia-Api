package protocol

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/http/httpguts"
)

const headerControlsPath = "/mapping/headerControls"

// ProtocolHeaderControls maps explicitly selected comma-separated header tokens
// into core request controls. Header names are lowercase; token values retain case.
type ProtocolHeaderControls struct {
	Headers []string   `json:"headers"`
	Program Expression `json:"program"`
}

// RequestHeaderControls carries semantic intent decoded from request headers.
type RequestHeaderControls struct {
	IsInterleavedThinking bool `json:"interleavedThinking"`
}

type compiledHeaderControls struct {
	headers []string
	program *compiledOutputPlan
}

func (compiled *Compiled) compileHeaderControls(declaration *ProtocolHeaderControls, compiler *expressionCompiler) error {
	if declaration == nil {
		return nil
	}
	if len(declaration.Headers) == 0 || len(declaration.Headers) > compiled.limits.StateItems {
		return fmt.Errorf("%s/headers: requires a bounded, nonempty header selection", headerControlsPath)
	}
	input := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{}}
	for _, name := range declaration.Headers {
		if _, exists := input.Properties[name]; exists || name != strings.ToLower(name) || !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("%s/headers: requires unique lowercase HTTP header names", headerControlsPath)
		}
		input.Properties[name] = ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: StringType}}
	}
	program, err := compiler.compilePlan(declaration.Program, headerControlsPath+"/program", expressionScope{input: input, root: input}, 1)
	if err != nil {
		return err
	}
	if !isAssignable(program.expression.result, ObjectType) {
		return fmt.Errorf("%s/program: requires a request header controls object", headerControlsPath)
	}
	compiled.packagePlan.headerControls = &compiledHeaderControls{headers: append([]string(nil), declaration.Headers...), program: program}
	for _, name := range sortedKeys(compiled.operations) {
		operation := compiled.operations[name]
		headers := map[string][]string{}
		for _, key := range sortedKeys(operation.Headers) {
			lower := strings.ToLower(key)
			headers[lower] = append(headers[lower], operation.Headers[key])
		}
		controls, err := compiled.ResolveHeaderControls(context.Background(), func(name string) string { return strings.Join(headers[name], ",") })
		if err != nil {
			return fmt.Errorf("/spec/operations/%s/headers: %w", escapePointer(name), err)
		}
		operation.IsInterleavedThinking = controls.IsInterleavedThinking
		compiled.operations[name] = operation
	}
	return nil
}

// ResolveHeaderControls reads only Mapping.headerControls.headers through get.
// The host supplies actual received headers; context JSON cannot replace them.
func (compiled *Compiled) ResolveHeaderControls(ctx context.Context, get func(string) string) (RequestHeaderControls, error) {
	if err := ctx.Err(); err != nil {
		return RequestHeaderControls{}, err
	}
	controls := compiled.packagePlan.headerControls
	if controls == nil {
		return RequestHeaderControls{}, nil
	}
	headers := make(map[string][]string, len(controls.headers))
	bytes := 0
	for _, name := range controls.headers {
		value := get(name)
		bytes += len(name) + len(value)
		if bytes > compiled.limits.BufferBytes {
			return RequestHeaderControls{}, resourceFailure("header_controls", "bytes", compiled.limits.BufferBytes, bytes)
		}
		tokens := []string{}
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				tokens = append(tokens, token)
			}
		}
		headers[name] = tokens
	}
	input, err := EncodeValue(headers)
	if err != nil {
		return RequestHeaderControls{}, err
	}
	if err := checkResourceValue(input, compiled.limits, "header_controls"); err != nil {
		return RequestHeaderControls{}, err
	}
	state := evaluation{ctx: ctx, input: input, root: input, budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	output, err := controls.program.evaluate(state)
	if err != nil {
		return RequestHeaderControls{}, locateMappingError(headerControlsPath, err)
	}
	fields, err := output.ReadObject()
	if err != nil || len(fields) != 1 {
		return RequestHeaderControls{}, fmt.Errorf("%s/program: requires only interleavedThinking", headerControlsPath)
	}
	isInterleaved, err := readBoolean(fields["interleavedThinking"])
	if err != nil {
		return RequestHeaderControls{}, fmt.Errorf("%s/program/interleavedThinking: %w", headerControlsPath, err)
	}
	return RequestHeaderControls{IsInterleavedThinking: isInterleaved}, nil
}
