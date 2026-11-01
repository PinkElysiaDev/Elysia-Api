package protocol

import (
	"fmt"
	"slices"
)

// ProtocolAgentMapping describes preferences and local tool-result rendering.
// The parameters program cannot change history, authorization or transport.
type ProtocolAgentMapping struct {
	ToolResults     InputKind             `json:"toolResults"`
	ThinkingEfforts []string              `json:"thinkingEfforts"`
	Parameters      Expression            `json:"parameters"`
	ClientOutput    map[string]Expression `json:"clientOutput,omitempty"`
	Samples         []AgentSample         `json:"samples"`
}

type compiledAgentOutputField struct {
	program *compiledOutputPlan
	set     func(*ClientOutput, JSONOutput, *valueReader) error
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
	if len(configuration.ClientOutput) > 0 {
		for _, sample := range configuration.Samples {
			if sample.ExpectedClientOutput.IsZero() {
				return fmt.Errorf("/mapping/agent/samples/%s: client output policy requires an expectedClientOutput", sample.ID)
			}
		}
	}
	for _, name := range sortedKeys(configuration.ClientOutput) {
		path := "/mapping/agent/clientOutput/" + name
		setter, kind, err := compileAgentOutputSetter(name)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		field, err := expressions.compilePlan(configuration.ClientOutput[name], path, expressionScope{}, 1)
		if err != nil {
			return err
		}
		if !isAssignable(field.expression.result, kind) {
			return fmt.Errorf("%s: declaration has the wrong client output type", path)
		}
		compiled.agentClientOutput = append(compiled.agentClientOutput, compiledAgentOutputField{program: field, set: setter})
	}
	compiled.agentMapping = &compiledMapping{outputPlan: plan}
	compiled.agentEfforts, compiled.agentToolResults = slices.Clone(configuration.ThinkingEfforts), configuration.ToolResults
	return nil
}

// HasAgentPolicy reports whether this pinned revision supplies an Agent program.
// Activation evidence is checked by the registry, independently of this query.
func (compiled *Compiled) HasAgentPolicy() bool { return compiled.agentMapping != nil }

func compileAgentOutputSetter(name string) (func(*ClientOutput, JSONOutput, *valueReader) error, JSONType, error) {
	switch name {
	case "includeUsage":
		return func(output *ClientOutput, value JSONOutput, reader *valueReader) error {
			return writeCoreBoolPointer(value, &output.IncludeUsage, reader)
		}, BooleanType, nil
	case "rawStreamOptions":
		return func(output *ClientOutput, value JSONOutput, reader *valueReader) error {
			return assignCoreValue(value, &output.RawStreamOptions, reader)
		}, AnyType, nil
	case "rawResponsesInclude":
		return func(output *ClientOutput, value JSONOutput, reader *valueReader) error {
			return assignCoreValue(value, &output.RawResponsesInclude, reader)
		}, AnyType, nil
	case "responsesStorage":
		return func(output *ClientOutput, value JSONOutput, reader *valueReader) error {
			var storage *ResponsesStorageIntent
			if err := writeCoreResponsesStorageIntentPointer(value, &storage, reader); err != nil {
				return err
			}
			output.ResponsesStorage = storage
			return nil
		}, ObjectType, nil
	default:
		return nil, "", fmt.Errorf("agent policy cannot write this client output field")
	}
}
