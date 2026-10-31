package protocol

import (
	"context"
	"fmt"
	"slices"
)

// ProtocolModelPolicy resolves model-dependent semantic constraints. Its input
// is the selected outgoing model name. Every result branch selects a literal
// capability profile, validated once when the package is compiled.
type ProtocolModelPolicy struct {
	Reasoning Expression            `json:"reasoning"`
	Steps     []ProtocolControlStep `json:"steps,omitempty"`
	Checks    []ProtocolControlStep `json:"checks,omitempty"`
}

type compiledModelPolicy struct {
	reasoning  *compiledOutputPlan
	profiles   map[Value]ReasoningCapabilities
	steps      []compiledControlStep
	checks     []compiledControlStep
	conversion Value
}

func (compiled *Compiled) compileModelPolicy(policy *ProtocolModelPolicy, expressions *expressionCompiler) error {
	if policy == nil {
		return nil
	}
	references := map[string]Expression{}
	previous := expressions.mappingReferences
	expressions.mappingReferences = references
	defer func() { expressions.mappingReferences = previous }()
	model, err := compileModelPolicy(policy, expressions)
	if err != nil {
		return err
	}
	if len(policy.Steps) != 0 {
		model.conversion, err = EncodeValue(modelControlOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, Policy: *policy, Expressions: references})
		if err != nil {
			return err
		}
	}
	compiled.packagePlan.modelPolicy = model
	return nil
}

func compileModelPolicy(policy *ProtocolModelPolicy, expressions *expressionCompiler) (*compiledModelPolicy, error) {
	input := &ValueSchema{Type: StringType}
	plan, err := expressions.compilePlan(policy.Reasoning, "/mapping/modelPolicy/reasoning", expressionScope{input: input, root: input}, 1)
	if err != nil {
		return nil, err
	}
	model := &compiledModelPolicy{reasoning: plan, profiles: map[Value]ReasoningCapabilities{}}
	if err := model.collectProfiles(plan); err != nil {
		return nil, err
	}
	model.steps, err = compileControlSteps(policy.Steps, expressions, "/mapping/modelPolicy/steps", false)
	if err == nil {
		model.checks, err = compileControlSteps(policy.Checks, expressions, "/mapping/modelPolicy/checks", true)
	}
	return model, err
}

func (model *compiledModelPolicy) collectProfiles(plan *compiledOutputPlan) error {
	if plan == nil {
		return fmt.Errorf("model reasoning policy requires an explicit result for every branch")
	}
	switch plan.expression.op {
	case "let":
		return model.collectProfiles(plan.body)
	case "if":
		if err := model.collectProfiles(plan.then); err != nil {
			return err
		}
		return model.collectProfiles(plan.otherwise)
	case "choose":
		for _, name := range sortedKeys(plan.cases) {
			if err := model.collectProfiles(plan.cases[name]); err != nil {
				return err
			}
		}
		return model.collectProfiles(plan.otherwise)
	case "literal":
		var capability ReasoningCapabilities
		if err := decodeContract(plan.expression.value.Bytes(), &capability); err != nil {
			return fmt.Errorf("%s: %w", plan.expression.path, err)
		}
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("%s: %w", plan.expression.path, err)
		}
		model.profiles[plan.expression.value] = capability
		return nil
	default:
		return fmt.Errorf("%s: model policy results must select declared literal capability profiles", plan.expression.path)
	}
}

// ResolveModelReasoning evaluates the selected revision's model policy. An
// explicit binding replaces the inferred capability as a whole. Unconfigured
// packages remain unknown; family labels never select an installed codec.
func (compiled *Compiled) ResolveModelReasoning(ctx context.Context, model string, bound *ReasoningCapabilities) (ReasoningCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return ReasoningCapabilities{}, err
	}
	if compiled.packagePlan == nil && bound == nil {
		return ResolveReasoningCapabilities(compiled.Codec(EncodeRequest), model), nil
	}
	var policy *compiledModelPolicy
	if compiled.packagePlan != nil {
		policy = compiled.packagePlan.modelPolicy
	}
	return policy.resolveReasoning(ctx, model, bound, compiled.limits)
}

func (policy *compiledModelPolicy) resolveReasoning(ctx context.Context, model string, bound *ReasoningCapabilities, limits Limits) (ReasoningCapabilities, error) {
	if bound != nil {
		if err := bound.Validate(); err != nil {
			return ReasoningCapabilities{}, err
		}
		return cloneReasoningCapability(*bound), nil
	}
	if policy == nil {
		return ReasoningCapabilities{Kind: "unknown"}, nil
	}
	input := StringValue(model)
	output, err := policy.reasoning.evaluate(evaluation{ctx: ctx, input: input, root: input, budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}})
	if err != nil {
		return ReasoningCapabilities{}, err
	}
	return cloneReasoningCapability(policy.profiles[output]), nil
}

func cloneReasoningCapability(capability ReasoningCapabilities) ReasoningCapabilities {
	capability.Levels = slices.Clone(capability.Levels)
	capability.DisabledLevels = slices.Clone(capability.DisabledLevels)
	capability.BetweenToolsLevels = slices.Clone(capability.BetweenToolsLevels)
	return capability
}
