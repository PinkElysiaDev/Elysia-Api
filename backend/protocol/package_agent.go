package protocol

import (
	"fmt"
	"slices"
)

// ProtocolAgentMapping describes preferences and local tool-result rendering.
// The parameters program cannot change history, authorization or transport.
type ProtocolAgentMapping struct {
	ToolResults     InputKind     `json:"toolResults"`
	ThinkingEfforts []string      `json:"thinkingEfforts"`
	Parameters      Expression    `json:"parameters"`
	Samples         []AgentSample `json:"samples"`
}

func (compiled *Compiled) compilePackageAgent(configuration *ProtocolAgentMapping, expressions *expressionCompiler) error {
	if configuration == nil {
		return nil
	}
	if err := compiled.checkAgentRequirements(configuration.ToolResults); err != nil {
		return fmt.Errorf("/mapping/agent: %w", err)
	}
	plan, err := expressions.compilePlan(configuration.Parameters, "/mapping/agent/parameters", expressionScope{}, 1)
	if err != nil {
		return err
	}
	if err := checkExpressionOutput(plan.expression, &ValueSchema{Type: ObjectType, AllowUnknown: true}, compiled.limits); err != nil {
		return err
	}
	if err := checkAgentEvidence(configuration.ThinkingEfforts, configuration.Samples); err != nil {
		return fmt.Errorf("/mapping/agent: %w", err)
	}
	compiled.agentMapping = &compiledMapping{outputPlan: plan}
	compiled.agentEfforts, compiled.agentToolResults = slices.Clone(configuration.ThinkingEfforts), configuration.ToolResults
	return nil
}

// HasAgentPolicy reports whether this pinned revision supplies an Agent program.
// Activation evidence is checked by the registry, independently of this query.
func (compiled *Compiled) HasAgentPolicy() bool { return compiled.agentMapping != nil }
