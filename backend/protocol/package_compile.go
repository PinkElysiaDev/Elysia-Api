package protocol

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type compiledPackagePlan struct {
	coreVersion          int
	dslVersion           int
	effects              map[Direction][]compiledSemanticEffect
	requestProjection    Value
	hasCacheProjection   bool
	hasHistoryProjection bool
}

type compiledSemanticEffect struct {
	id, kind, path, target, reason string
	corePointer, wirePointer       []string
	when                           *compiledOutputPlan
	layout                         *ProtocolAggregateLayout
	valueMapping                   *ProtocolValueMapping
	valueUnmatched                 []string
	scope                          string
	severity                       Severity
	conditionReferences            map[string]Expression
}

// DecodeProtocolPackage parses and validates a package without activating it.
// Validation and executable compilation share the same contract compiler.
func DecodeProtocolPackage(raw []byte, limits Limits) (ProtocolPackage, error) {
	document, err := decodeProtocolPackage(raw, limits)
	if err != nil {
		return document, err
	}
	return document, document.Validate(limits)
}

// Validate checks package contracts and programs without granting execution or
// fidelity evidence. Some declared effects require a later runtime revision.
func (document ProtocolPackage) Validate(limits Limits) error {
	_, err := document.compileContract(limits)
	return err
}

// CompilePackage compiles the v3 format independently of legacy codec modules.
// Unsupported semantic effects are rejected instead of silently ignored.
func (compiler *Compiler) CompilePackage(raw []byte) (*Compiled, []ConversionIssue) {
	document, err := decodeProtocolPackage(raw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/", err)
	}
	compiled, err := document.compileContract(compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/", err)
	}
	for _, direction := range sortedKeys(compiled.packagePlan.effects) {
		if isEventDirection(direction) {
			return nil, definitionIssues(compiled.identity, "/mapping/directions/"+string(direction), fmt.Errorf("event programs require the core event runtime and are not executable yet"))
		}
		if compiled.mappings[direction].outputPlan == nil {
			return nil, definitionIssues(compiled.identity, "/mapping/directions/"+string(direction)+"/program", fmt.Errorf("program requires a compiled output plan"))
		}
		for index, effect := range compiled.packagePlan.effects[direction] {
			if effect.kind != "emit" && effect.kind != "reject" && effect.kind != "aggregate" && !isRequestProjectionScope(effect.scope) {
				path := fmt.Sprintf("/mapping/directions/%s/effects/%d/kind", direction, index)
				return nil, definitionIssues(compiled.identity, path, fmt.Errorf("semantic effect %q is not executable in this runtime", effect.kind))
			}
			if effect.kind == "aggregate" {
				path := fmt.Sprintf("/mapping/directions/%s/effects/%d", direction, index)
				if effect.valueMapping != nil {
					if !compiled.mappings[direction].capabilities[ReasoningHistoryRetentionCapability] || !provesHistoryValueMapping(compiled.mappings[direction].outputPlan.expression, direction, effect) {
						return nil, definitionIssues(compiled.identity, path, fmt.Errorf("history value mapping requires declared capability and a provable whole-value partition or reconstruction"))
					}
					continue
				}
				if !compiled.mappings[direction].capabilities[CacheBreakpointsCapability] {
					return nil, definitionIssues(compiled.identity, path, fmt.Errorf("cache aggregate requires the declared cache.breakpoints capability"))
				}
				if !provesPackageAggregate(compiled.mappings[direction].outputPlan.expression, direction, effect) {
					return nil, definitionIssues(compiled.identity, path, fmt.Errorf("aggregate requires a provable object partition or reconstruction"))
				}
			}
			if effect.kind == "emit" {
				input, output := effect.corePointer, effect.wirePointer
				if isPackageDecodeDirection(direction) {
					input, output = output, input
				}
				if !provesPackageEmission(compiled.mappings[direction].outputPlan.expression, output, input) {
					path := fmt.Sprintf("/mapping/directions/%s/effects/%d", direction, index)
					return nil, definitionIssues(compiled.identity, path, fmt.Errorf("emit effect %q requires a provable read of its declared input field", effect.id))
				}
			}
		}
	}
	if err := compiled.bindRequestProjection(document); err != nil {
		return nil, definitionIssues(compiled.identity, "/mapping/directions/encode_request/effects", err)
	}
	return compiled, nil
}

// Package returns an independent v3 document. Legacy definitions return false.
func (compiled *Compiled) Package() (ProtocolPackage, bool) {
	var document ProtocolPackage
	if compiled.packagePlan == nil {
		return document, false
	}
	_ = compiled.definition.Decode(&document)
	return document, true
}

func (compiled *Compiled) bindPackageIdentity(document ProtocolPackage) error {
	value, err := EncodeValue(document)
	if err != nil {
		return err
	}
	compiled.definition = value
	identity, err := EncodeValue(struct {
		CompilerVersion string          `json:"compilerVersion"`
		Package         ProtocolPackage `json:"package"`
	}{CompilerVersion, document})
	if err != nil {
		return err
	}
	compiled.hash = hashValue(identity)
	samples, err := EncodeValue(struct {
		CoreVersion int      `json:"coreVersion"`
		DSLVersion  int      `json:"dslVersion"`
		Samples     []Sample `json:"samples"`
	}{document.Mapping.CoreVersion, document.Mapping.DSLVersion, document.Mapping.Samples})
	if err != nil {
		return err
	}
	compiled.samplesHash = hashValue(samples)
	return nil
}

func compileProtocolProgram(mapping ProtocolDirectionMapping, direction Direction, wire WireContractSpec, compiler *expressionCompiler, coreVersion, dslVersion int) (compiledMapping, []compiledSemanticEffect, error) {
	path := "/mapping/directions/" + string(direction)
	isDecode := isPackageDecodeDirection(direction)
	scope := expressionScope{canReadRuntime: true}
	if isDecode {
		scope.input = wire.Schema
	}
	references := map[string]Expression{}
	compiler.mappingReferences = references
	defer func() { compiler.mappingReferences = nil }()
	programScope := scope
	programScope.wireCapture = isDecode
	programScope.canReadNative = !isDecode
	program, err := compiler.compile(mapping.Program, path+"/program", programScope, 1)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	if !isDecode {
		if err := checkExpressionOutput(program, wire.Schema, compiler.limits); err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/program: %w", path, err)
		}
	}
	if len(mapping.Effects) == 0 || len(mapping.Effects) > compiler.limits.Mutations {
		return compiledMapping{}, nil, fmt.Errorf("%s/effects: requires a nonempty bounded effect list", path)
	}
	seen := map[string]bool{}
	effects := make([]compiledSemanticEffect, 0, len(mapping.Effects))
	for index, effect := range mapping.Effects {
		at := fmt.Sprintf("%s/effects/%d", path, index)
		if !definitionIdentifier.MatchString(effect.ID) || seen[effect.ID] {
			return compiledMapping{}, nil, fmt.Errorf("%s/id: expected a unique effect identifier", at)
		}
		seen[effect.ID] = true
		switch effect.Kind {
		case "emit", "aggregate", "reject":
		case "default", "drop":
			if isDecode {
				return compiledMapping{}, nil, fmt.Errorf("%s/kind: decode programs cannot default or drop input semantics", at)
			}
		default:
			return compiledMapping{}, nil, fmt.Errorf("%s/kind: unknown semantic effect %q", at, effect.Kind)
		}
		if (effect.Kind == "default" || effect.Kind == "drop" || effect.Kind == "reject") && strings.TrimSpace(effect.Reason) == "" {
			return compiledMapping{}, nil, fmt.Errorf("%s/reason: semantic disposition requires a reason", at)
		}
		if err := validateRequestProjectionEffect(direction, effect); err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s: %w", at, err)
		}
		entry := compiledSemanticEffect{id: effect.ID, kind: effect.Kind, path: effect.Path, target: effect.Target, reason: effect.Reason, scope: effect.Scope, severity: effect.Severity}
		entry.corePointer, err = parsePointer(effect.Path)
		if err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/path: %w", at, err)
		}
		entry.wirePointer, err = parsePointer(effect.Target)
		if err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/target: %w", at, err)
		}
		if effect.Kind == "aggregate" {
			if effect.ValueMapping != nil {
				if effect.Layout != nil {
					return compiledMapping{}, nil, fmt.Errorf("%s: aggregate cannot combine object and value mappings", at)
				}
				if err := validateHistoryValueMapping(direction, effect.Path, effect.ValueMapping, compiler.limits); err != nil {
					return compiledMapping{}, nil, fmt.Errorf("%s/valueMapping: %w", at, err)
				}
				entry.valueMapping = effect.ValueMapping
				entry.valueUnmatched, _ = parsePointer(effect.ValueMapping.Unmatched)
			} else if err := validatePackageAggregateLayout(direction, entry.corePointer, effect.Layout); err != nil {
				return compiledMapping{}, nil, fmt.Errorf("%s/layout: %w", at, err)
			}
			entry.layout = effect.Layout
		} else if effect.Layout != nil || effect.ValueMapping != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s: only aggregate effects have an object or value mapping", at)
		}
		if effect.When != nil {
			conditionScope := scope
			if isRequestProjectionScope(effect.Scope) {
				conditionScope = expressionScope{}
				entry.conditionReferences = map[string]Expression{}
				compiler.mappingReferences = entry.conditionReferences
			}
			condition, err := compiler.compile(*effect.When, at+"/when", conditionScope, 1)
			compiler.mappingReferences = references
			for name, expression := range entry.conditionReferences {
				references[name] = expression
			}
			if err != nil {
				return compiledMapping{}, nil, err
			}
			if !isAssignable(condition.result, BooleanType) {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: expected a boolean expression", at)
			}
			var hasPlan bool
			entry.when, hasPlan = compileOutputPlan(condition)
			if !hasPlan {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: condition requires a compiled output plan", at)
			}
			if isRequestProjectionScope(effect.Scope) && !localProjectionCondition(entry.when) {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: projection conditions may read only their local input or root", at)
			}
		}
		effects = append(effects, entry)
	}
	identity, err := EncodeValue(struct {
		CompilerVersion string                   `json:"compilerVersion"`
		CoreVersion     int                      `json:"coreVersion"`
		DSLVersion      int                      `json:"dslVersion"`
		Direction       Direction                `json:"direction"`
		Wire            WireContractSpec         `json:"wire"`
		Mapping         ProtocolDirectionMapping `json:"mapping"`
		References      map[string]Expression    `json:"references"`
	}{CompilerVersion, coreVersion, dslVersion, direction, wire, mapping, references})
	if err != nil {
		return compiledMapping{}, nil, err
	}
	plan, hasPlan := compileOutputPlan(program)
	if !hasPlan {
		return compiledMapping{}, nil, fmt.Errorf("%s/program: expression has no compiled execution plan", path)
	}
	return compiledMapping{
		outputPlan: plan, definitionHash: hashValue(identity),
		hasCoreOutputPlan: direction == DecodeRequest || direction == DecodeResponse,
		hasCoreInputPlan:  direction == EncodeRequest || direction == EncodeResponse,
	}, effects, nil
}

func isPackageDecodeDirection(direction Direction) bool {
	return direction == DecodeRequest || direction == DecodeResponse || direction == DecodeEvent
}

// An emit is a field copy, not an author-supplied claim that arbitrary code is
// lossless. Resolve constructors once so execution never compares large values.
func provesPackageEmission(program *compiledExpression, output, input []string) bool {
	if program == nil {
		return false
	}
	switch program.op {
	case "read":
		return program.from == "input" && len(input) == len(program.pointer)+len(output) &&
			slices.Equal(input[:len(program.pointer)], program.pointer) && slices.Equal(input[len(program.pointer):], output)
	case "if":
		return provesPackageEmission(program.then, output, input) && provesPackageEmission(program.otherwise, output, input)
	case "choose":
		if !provesPackageEmission(program.otherwise, output, input) {
			return false
		}
		for _, branch := range program.cases {
			if !provesPackageEmission(branch, output, input) {
				return false
			}
		}
		return true
	}
	if len(output) == 0 {
		return false
	}
	switch program.op {
	case "object":
		return provesPackageEmission(program.fields[output[0]], output[1:], input)
	case "array":
		index, err := strconv.Atoi(output[0])
		if err != nil || index < 0 || index >= len(program.items) || strconv.Itoa(index) != output[0] {
			return false
		}
		for _, item := range program.items[:index] {
			if !alwaysPresentOutput(item) {
				return false
			}
		}
		return provesPackageEmission(program.items[index], output[1:], input)
	}
	return false
}

func alwaysPresentOutput(program *compiledExpression) bool {
	if program == nil {
		return false
	}
	switch program.op {
	case "object", "array", "literal":
		return true
	case "read":
		return program.isRequired
	case "if":
		return alwaysPresentOutput(program.then) && alwaysPresentOutput(program.otherwise)
	case "choose":
		if !alwaysPresentOutput(program.otherwise) {
			return false
		}
		for _, branch := range program.cases {
			if !alwaysPresentOutput(branch) {
				return false
			}
		}
		return true
	}
	return false
}
