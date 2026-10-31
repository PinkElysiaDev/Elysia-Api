package protocol

import (
	"fmt"
	"reflect"
)

// ProtocolConversionDefault declares one complete overridable target rule.
type ProtocolConversionDefault struct {
	ID         string                        `json:"id"`
	Order      int                           `json:"order"`
	Enabled    bool                          `json:"enabled"`
	Phase      ConversionPhase               `json:"phase"`
	Match      ConversionMatch               `json:"match,omitempty"`
	Steps      []ProtocolControlStep         `json:"steps,omitempty"`
	Auxiliary  *ProtocolAuxiliaryDisposition `json:"auxiliary,omitempty"`
	Control    *ProtocolControlDisposition   `json:"control,omitempty"`
	Collection bool                          `json:"collection,omitempty"`
}

type coreControlOptions struct {
	CoreVersion    int                   `json:"coreVersion"`
	Target         Identity              `json:"target"`
	MappingHash    string                `json:"mappingHash"`
	Limits         Limits                `json:"limits"`
	ExplicitLimits bool                  `json:"explicitLimits"`
	Phase          ConversionPhase       `json:"phase"`
	Steps          []ProtocolControlStep `json:"steps"`
	Expressions    map[string]Expression `json:"expressions,omitempty"`
	Collection     bool                  `json:"collection,omitempty"`
	steps          []compiledControlStep
	bindingSlots   int
	hasContext     bool
}

type preparedConversionOption struct {
	declarationHash string
	phase           ConversionPhase
	limits          Limits
	option          conversionOption
}

func (compiled *Compiled) compileConversionDefaults(mapping ProtocolMapping, compiler *expressionCompiler) error {
	if len(mapping.ConversionDefaults) > compiler.limits.Mutations {
		return fmt.Errorf("/mapping/conversionDefaults: exceeds rule limit")
	}
	seen, orders := map[string]bool{}, map[string]bool{}
	for index, declaration := range mapping.ConversionDefaults {
		path := fmt.Sprintf("/mapping/conversionDefaults/%d", index)
		if !definitionIdentifier.MatchString(declaration.ID) || seen[declaration.ID] {
			return fmt.Errorf("%s/id: invalid or duplicate rule identity", path)
		}
		seen[declaration.ID] = true
		order := fmt.Sprintf("%s:%d", declaration.Phase, declaration.Order)
		if declaration.Enabled && orders[order] {
			return fmt.Errorf("%s/order: ambiguous rule order", path)
		}
		orders[order] = declaration.Enabled || orders[order]
		if declaration.Control != nil {
			rule, err := compiled.compileControlDefault(declaration)
			if err != nil {
				return locateMappingError(path, err)
			}
			compiled.packagePlan.conversionDefaults = append(compiled.packagePlan.conversionDefaults, rule)
			continue
		}
		if declaration.Auxiliary != nil {
			rule, err := compiled.compileAuxiliaryDefault(declaration)
			if err != nil {
				return locateMappingError(path, err)
			}
			compiled.packagePlan.conversionDefaults = append(compiled.packagePlan.conversionDefaults, rule)
			continue
		}
		root, err := coreControlRoot(declaration.Phase)
		if err != nil {
			return locateMappingError(path+"/phase", err)
		}
		direction := controlDirection(declaration.Phase, false)
		program, exists := compiled.mappings[direction]
		if !exists || declaration.Match.NodeKind != "" || declaration.Collection && declaration.Phase != ConversionResponse {
			return fmt.Errorf("%s: requires an implemented target direction and whole-document controls", path)
		}
		references := map[string]Expression{}
		previous := compiler.mappingReferences
		compiler.mappingReferences = references
		steps, err := compileTypedControlSteps(declaration.Steps, compiler, path+"/steps", false, root)
		compiler.mappingReferences = previous
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			return fmt.Errorf("%s/steps: requires a control step", path)
		}
		options := coreControlOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, MappingHash: program.definitionHash, Limits: compiler.limits, ExplicitLimits: compiled.explicitLimits,
			Phase: declaration.Phase, Steps: declaration.Steps, Expressions: references, Collection: declaration.Collection,
			steps: steps, bindingSlots: controlBindingSlots(steps), hasContext: controlsReadContext(steps)}
		value, err := EncodeValue(options)
		if err != nil {
			return err
		}
		rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "core_controls", Value: value}
		if err := validateCoreControlRule(rule); err != nil {
			return locateMappingError(path, err)
		}
		fingerprint, err := conversionRuleFingerprint(rule)
		if err != nil {
			return err
		}
		rule.preparedOption = &preparedConversionOption{declarationHash: fingerprint, phase: rule.Phase, limits: compiler.limits, option: conversionOption{raw: value, value: options}}
		compiled.packagePlan.conversionDefaults = append(compiled.packagePlan.conversionDefaults, rule)
	}
	return compiled.compileFieldObservations(mapping.FieldObservations)
}

func coreControlRoot(phase ConversionPhase) (reflect.Type, error) {
	switch phase {
	case ConversionRequest:
		return reflect.TypeFor[Request](), nil
	case ConversionResponse:
		return reflect.TypeFor[Response](), nil
	default:
		return nil, fmt.Errorf("core controls require request or response phase")
	}
}

func validateCoreControlRule(rule ConversionRule) error {
	if rule.Value.IsZero() || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" {
		return fmt.Errorf("core_controls requires a complete declaration and a whole-document match")
	}
	return nil
}

func compileCoreControlOption(rule ConversionRule, limits Limits) (conversionOption, error) {
	if prepared := rule.preparedOption; prepared != nil && prepared.phase == rule.Phase && prepared.limits == limits && prepared.option.raw == rule.Value {
		fingerprint, err := conversionRuleFingerprint(rule)
		if err != nil {
			return conversionOption{}, err
		}
		if fingerprint == prepared.declarationHash {
			return prepared.option, nil
		}
	}
	var options coreControlOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || options.MappingHash == "" || options.Phase != rule.Phase || len(options.Steps) == 0 || options.Collection && options.Phase != ConversionResponse {
		return conversionOption{}, fmt.Errorf("core controls require a pinned target, matching phase and nonempty steps")
	}
	if err := options.Limits.Validate(); err != nil {
		return conversionOption{}, err
	}
	root, err := coreControlRoot(options.Phase)
	if err != nil {
		return conversionOption{}, err
	}
	compiler := newExpressionCompiler(intersectControlLimits(limits, options.Limits), options.Expressions)
	options.steps, err = compileTypedControlSteps(options.Steps, compiler, "/rules/"+rule.ID+"/value/steps", false, root)
	if err != nil {
		return conversionOption{}, err
	}
	for name := range options.Expressions {
		if !compiler.used[name] {
			return conversionOption{}, fmt.Errorf("core control expression %q is unreachable", name)
		}
	}
	options.bindingSlots = controlBindingSlots(options.steps)
	options.hasContext = controlsReadContext(options.steps)
	return conversionOption{raw: rule.Value, value: options}, nil
}

func intersectControlLimits(left, right Limits) Limits {
	return Limits{Depth: min(left.Depth, right.Depth), Nodes: min(left.Nodes, right.Nodes), Mutations: min(left.Mutations, right.Mutations), StateItems: min(left.StateItems, right.StateItems), BufferBytes: min(left.BufferBytes, right.BufferBytes)}
}

func controlsReadContext(steps []compiledControlStep) bool {
	var reads func(*compiledOutputPlan) bool
	reads = func(plan *compiledOutputPlan) bool {
		if plan == nil {
			return false
		}
		if plan.expression.op == "read" && plan.expression.from == "context" {
			return true
		}
		for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
			if reads(child) {
				return true
			}
		}
		for _, child := range plan.items {
			if reads(child) {
				return true
			}
		}
		for _, field := range plan.fields {
			if reads(field.expression) {
				return true
			}
		}
		for _, child := range plan.cases {
			if reads(child) {
				return true
			}
		}
		return false
	}
	for _, step := range steps {
		if reads(step.when) || step.diagnostic != nil && reads(step.diagnostic.reason) {
			return true
		}
		for _, update := range step.updates {
			if reads(update.program) {
				return true
			}
		}
	}
	return false
}

func conversionRuleFingerprint(rule ConversionRule) (string, error) {
	value, err := EncodeValue(rule)
	if err != nil {
		return "", err
	}
	return hashValue(value), nil
}

func (compiled *Compiled) defaultConversionRules(phase ConversionPhase) []ConversionRule {
	if compiled == nil || compiled.packagePlan == nil {
		return nil
	}
	var rules []ConversionRule
	for _, rule := range compiled.packagePlan.conversionDefaults {
		if rule.Phase == phase {
			rules = append(rules, rule)
		}
	}
	return rules
}
