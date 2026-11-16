package protocol

import (
	"fmt"
	"strings"
)

// ProtocolSourceFrameMatch selects a narrow frame shape for normalization.
// Matching does not replace the independent source Spec contract.
type ProtocolSourceFrameMatch struct {
	Schema ValueSchema `json:"schema"`
	When   *Expression `json:"when,omitempty"`
}

// ProtocolSourcePrelude permits one opening frame to be corrected from the next.
// When and Set read the next frame as input and the retained first frame as root.
type ProtocolSourcePrelude struct {
	First  ProtocolSourceFrameMatch `json:"first"`
	Next   ProtocolSourceFrameMatch `json:"next"`
	When   Expression               `json:"when"`
	Set    []ProtocolSourceUpdate   `json:"set"`
	Reason string                   `json:"reason"`
}

// ProtocolSourceUpdate replaces one existing field from the original frame pair.
// Declaration order determines diagnostic order; updates cannot overlap.
type ProtocolSourceUpdate struct {
	Path  string     `json:"path"`
	Value Expression `json:"value"`
}

type compiledSourceUpdate struct {
	path    string
	pointer []string
	value   *compiledOutputPlan
}

type compiledSourceFrameMatch struct {
	schema *wireSchemaPlan
	when   *compiledOutputPlan
}

type sourcePreludeOptions struct {
	Source       Identity              `json:"source"`
	DecoderHash  string                `json:"decoderHash"`
	Limits       Limits                `json:"limits"`
	Prelude      ProtocolSourcePrelude `json:"prelude"`
	Expressions  map[string]Expression `json:"expressions,omitempty"`
	first, next  compiledSourceFrameMatch
	when         *compiledOutputPlan
	updates      []compiledSourceUpdate
	bindingSlots int
}

func (compiled *Compiled) compileSourcePrelude(declaration ProtocolConversionDefault, compiler *expressionCompiler) (ConversionRule, error) {
	if declaration.Phase != ConversionSourceEvent || declaration.Use != "" || declaration.Recovery != nil || declaration.StreamDisposition != nil || declaration.Prelude != nil || declaration.Steps != nil || declaration.Auxiliary != nil || declaration.Control != nil || declaration.Collection || declaration.FrameUsage {
		return ConversionRule{}, fmt.Errorf("sourcePrelude requires a standalone source_event rule")
	}
	options := sourcePreludeOptions{Source: compiled.identity, DecoderHash: compiled.mappings[DecodeEvent].definitionHash, Limits: compiled.limits, Prelude: *declaration.SourcePrelude}
	previous := compiler.mappingReferences
	compiler.mappingReferences = map[string]Expression{}
	err := options.compile(compiler)
	options.Expressions = compiler.mappingReferences
	compiler.mappingReferences = previous
	if err != nil {
		return ConversionRule{}, err
	}
	value, err := EncodeValue(options)
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "source_prelude", Value: value}
	if err := validateSourcePreludeRule(rule); err != nil {
		return ConversionRule{}, err
	}
	fingerprint, err := conversionRuleFingerprint(rule)
	if err != nil {
		return ConversionRule{}, err
	}
	rule.preparedOption = &preparedConversionOption{phase: rule.Phase, limits: compiler.limits, declarationHash: fingerprint, option: conversionOption{raw: value, value: options}}
	return rule, nil
}

func validateSourcePreludeRule(rule ConversionRule) error {
	if rule.Phase != ConversionSourceEvent || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" || rule.Match.Path != "" || rule.Match.Present != nil {
		return fmt.Errorf("source_prelude requires a complete source rule with route conditions only")
	}
	return nil
}

func compileSourcePreludeOption(rule ConversionRule, limits Limits) (conversionOption, error) {
	if err := validateSourcePreludeRule(rule); err != nil {
		return conversionOption{}, err
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
	var options sourcePreludeOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if err := options.Limits.Validate(); err != nil {
		return conversionOption{}, err
	}
	compiler := newExpressionCompiler(intersectControlLimits(limits, options.Limits), options.Expressions)
	if err := options.compile(compiler); err != nil {
		return conversionOption{}, err
	}
	for name := range options.Expressions {
		if !compiler.used[name] {
			return conversionOption{}, fmt.Errorf("source prelude expression %q is unreachable", name)
		}
	}
	compiler.releaseGraph()
	return conversionOption{raw: rule.Value, value: options}, nil
}

func (options *sourcePreludeOptions) compile(compiler *expressionCompiler) error {
	if options.Source.DefinitionID == "" || options.DecoderHash == "" || strings.TrimSpace(options.Prelude.Reason) == "" || len(options.Prelude.Set) == 0 || len(options.Prelude.Set) > compiler.limits.Mutations {
		return fmt.Errorf("sourcePrelude requires a pinned source decoder, bounded updates and an explicit reason")
	}
	var err error
	options.first, err = compileSourceFrameMatch(options.Prelude.First, compiler, "/sourcePrelude/first")
	if err != nil {
		return err
	}
	options.next, err = compileSourceFrameMatch(options.Prelude.Next, compiler, "/sourcePrelude/next")
	if err != nil {
		return err
	}
	options.when, err = compileSourcePreludeExpression(options.Prelude.When, compiler, "/sourcePrelude/when", true)
	if err != nil {
		return err
	}
	options.updates = make([]compiledSourceUpdate, 0, len(options.Prelude.Set))
	options.bindingSlots = options.when.bindingSlots
	for _, match := range []compiledSourceFrameMatch{options.first, options.next} {
		if match.when != nil {
			options.bindingSlots = max(options.bindingSlots, match.when.bindingSlots)
		}
	}
	for _, update := range options.Prelude.Set {
		pointer, err := parsePointer(update.Path)
		if err != nil || len(pointer) == 0 {
			return fmt.Errorf("sourcePrelude updates require non-root JSON pointers")
		}
		for _, previous := range options.updates {
			if update.Path == previous.path || strings.HasPrefix(update.Path, previous.path+"/") || strings.HasPrefix(previous.path, update.Path+"/") {
				return fmt.Errorf("sourcePrelude updates cannot overlap")
			}
		}
		plan, err := compileSourcePreludeExpression(update.Value, compiler, "/sourcePrelude/set"+update.Path, false)
		if err != nil {
			return err
		}
		options.updates = append(options.updates, compiledSourceUpdate{path: update.Path, pointer: pointer, value: plan})
		options.bindingSlots = max(options.bindingSlots, plan.bindingSlots)
	}
	return nil
}

func compileSourceFrameMatch(declaration ProtocolSourceFrameMatch, compiler *expressionCompiler, path string) (compiledSourceFrameMatch, error) {
	if declaration.Schema.Type != ObjectType {
		return compiledSourceFrameMatch{}, fmt.Errorf("%s/schema: a source prelude matches object frames", path)
	}
	schema, err := compileWireSchemaPlan(&declaration.Schema, compiler.limits)
	if err != nil {
		return compiledSourceFrameMatch{}, err
	}
	var when *compiledOutputPlan
	if declaration.When != nil {
		when, err = compileSourcePreludeExpression(*declaration.When, compiler, path+"/when", true)
	}
	return compiledSourceFrameMatch{schema: schema, when: when}, err
}

func compileSourcePreludeExpression(expression Expression, compiler *expressionCompiler, path string, isPredicate bool) (*compiledOutputPlan, error) {
	plan, err := compiler.compilePlan(expression, path, expressionScope{}, 1)
	if err != nil {
		return nil, err
	}
	if !localProjectionCondition(plan) || isPredicate && !isAssignable(plan.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s: requires a pure local expression with no execution authority", path)
	}
	return plan, nil
}
