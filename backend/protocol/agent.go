package protocol

import (
	"context"
	"fmt"
	"slices"
)

// AgentPreferences describes explicit gateway Agent settings. Protocol authors
// declare their target-specific policy instead of the caller guessing a vendor.
type AgentPreferences struct {
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	ThinkingEnabled bool   `json:"thinkingEnabled"`
	ThinkingEffort  string `json:"thinkingEffort"`
}

// AgentConfig declares how the native Agent supplies semantic model parameters.
// It cannot change messages, tool permissions, credentials or routing.
type AgentConfig struct {
	ToolResults     InputKind     `json:"toolResults"`
	ThinkingEfforts []string      `json:"thinkingEfforts"`
	Parameters      Mapping       `json:"parameters"`
	Samples         []AgentSample `json:"samples"`
}

// AgentSample asserts a settings-to-parameters policy and its wire preservation.
type AgentSample struct {
	ID          string           `json:"id"`
	Preferences AgentPreferences `json:"preferences"`
	Expected    Value            `json:"expected"`
}

// AgentIdentity marks locally authored history independently of any provider.
func AgentIdentity() Identity {
	return Identity{Family: "elysia-agent", WireVersion: "1", DefinitionID: "agent", Revision: "1"}
}

func (compiler *Compiler) compileAgentMapping(compiled *Compiled, definition Definition, expressions *expressionCompiler) error {
	configuration := definition.Agent
	if configuration == nil {
		return nil
	}
	if configuration.ToolResults != JSONInput && configuration.ToolResults != TextInput {
		return fmt.Errorf("Agent toolResults must declare json or text serialization")
	}
	if !definition.Capabilities[FunctionToolsCapability] || !hasDefinitionDirections(definition, EncodeRequest, DecodeRequest) {
		return fmt.Errorf("Agent configuration requires function tools and request encoding/decoding for offline validation")
	}
	hasGeneration := false
	for _, operation := range definition.Operations {
		hasGeneration = hasGeneration || (operation.Kind == "generate" && operation.Transport != WebSocket)
	}
	if !hasGeneration {
		return fmt.Errorf("Agent requires a synchronous HTTP generation operation")
	}
	mapping := configuration.Parameters
	if mapping.Transform == nil || mapping.Module != "" || len(mapping.Rules) > 0 || mapping.After != nil || mapping.FrameBatch || mapping.UnknownEvent != "" || mapping.Capabilities != nil {
		return fmt.Errorf("Agent parameters require an independent declarative transform")
	}
	entry, err := compiler.compileMapping(mapping, "/agent/parameters", EncodeRequest, expressions)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, effort := range configuration.ThinkingEfforts {
		if seen[effort] {
			return fmt.Errorf("Agent thinking efforts must be unique")
		}
		seen[effort] = true
	}
	seen = map[string]bool{}
	for _, sample := range configuration.Samples {
		if sample.ID == "" || seen[sample.ID] || sample.Expected.IsZero() {
			return fmt.Errorf("Agent samples require unique identities and expected parameters")
		}
		seen[sample.ID] = true
	}
	compiled.agentMapping, compiled.agentEfforts = &entry, append([]string(nil), configuration.ThinkingEfforts...)
	return nil
}

// BuildAgentRequest prepares locally authored Agent settings and tool results.
// Provider output nodes retain their source, signatures and native snapshots.
func (compiled *Compiled) BuildAgentRequest(ctx context.Context, request Request, preferences AgentPreferences) (*Request, error) {
	if request.Source != AgentIdentity() {
		return nil, streamIssue(InvalidInput, "/source", "Agent request requires the local author identity")
	}
	parameters, err := compiled.BuildAgentParameters(ctx, preferences)
	if err != nil {
		return nil, err
	}
	request.Parameters = parameters
	request.ClientOutput = &ClientOutput{CollectUsage: preferences.Stream}
	resultKind := compiled.Definition().Agent.ToolResults
	var prepare func([]Node) ([]Node, error)
	prepare = func(nodes []Node) ([]Node, error) {
		result := append([]Node(nil), nodes...)
		for index := range result {
			node := &result[index]
			if node.Kind == ToolResultNode && node.Source != nil && node.Source.Protocol == request.Source {
				if !node.Payload.IsObject() || node.Native != nil {
					return nil, streamIssue(InvalidInput, "/agent/toolResults", "locally authored Agent results require a JSON object and no native snapshot")
				}
				if resultKind == TextInput {
					node.Payload = StringValue(string(node.Payload.Bytes()))
				}
			}
			if len(node.Children) > 0 {
				children, err := prepare(node.Children)
				if err != nil {
					return nil, err
				}
				node.Children = children
			}
		}
		return result, nil
	}
	request.Content, err = prepare(request.Content)
	return &request, err
}

// BuildAgentParameters applies the verified author's policy without naming a
// preset or wire family. An undeclared effort is an error, never a downgrade.
func (compiled *Compiled) BuildAgentParameters(ctx context.Context, preferences AgentPreferences) (Object, error) {
	if compiled.agentMapping == nil {
		return nil, streamIssue(UnsupportedCapability, "/agent", "protocol has no verified native Agent parameter policy")
	}
	if preferences.MaxOutputTokens <= 0 {
		return nil, streamIssue(InvalidInput, "/agent/maxOutputTokens", "Agent requires a positive output budget")
	}
	if preferences.ThinkingEnabled && !slices.Contains(compiled.agentEfforts, preferences.ThinkingEffort) {
		return nil, streamIssue(UnsupportedCapability, "/agent/thinkingEffort", "protocol does not declare the selected Agent thinking effort")
	}
	hasTransport := false
	for _, operation := range compiled.operations {
		isStream := operation.Transport == SSE || operation.Transport == NDJSON
		hasTransport = hasTransport || (operation.Kind == "generate" && operation.Transport != WebSocket && isStream == preferences.Stream)
	}
	if !hasTransport {
		return nil, streamIssue(UnsupportedCapability, "/agent/stream", "protocol does not declare the selected Agent generation transport")
	}
	input, err := EncodeValue(preferences)
	if err != nil {
		return nil, err
	}
	output, err := compiled.executeOperationMapping(ctx, *compiled.agentMapping, EncodeRequest, "/agent/parameters", input, EvaluationContext{})
	if err != nil {
		return nil, err
	}
	return output.ReadObject()
}

func verifyAgentSamples(ctx context.Context, compiled *Compiled, report *VerificationReport) {
	configuration := compiled.Definition().Agent
	if configuration == nil {
		return
	}
	hasDisabled := false
	hasStream := false
	covered := map[string]bool{}
	for _, sample := range configuration.Samples {
		parameters, err := compiled.BuildAgentParameters(ctx, sample.Preferences)
		if err == nil {
			actual, encodeErr := EncodeValue(parameters)
			err = encodeErr
			if err == nil && !equalValues(actual, sample.Expected) {
				err = fmt.Errorf("Agent parameters differ from the declared expectation")
			}
		}
		if err == nil {
			err = verifyAgentParameterWire(ctx, compiled, parameters)
		}
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: EncodeRequest, Passed: err == nil})
		if err != nil {
			report.Issues = append(report.Issues, verificationIssue(compiled, EncodeRequest, "/agent/samples/"+sample.ID, VerificationMismatch, err.Error(), sample.ID))
			continue
		}
		if sample.Preferences.ThinkingEnabled {
			covered[sample.Preferences.ThinkingEffort] = true
		} else {
			hasDisabled = true
		}
		hasStream = hasStream || sample.Preferences.Stream
	}
	if !hasDisabled {
		report.Issues = append(report.Issues, verificationIssue(compiled, EncodeRequest, "/agent/samples", IncompleteCoverage, "Agent requires a passing fixture with thinking disabled", ""))
	}
	for _, effort := range configuration.ThinkingEfforts {
		if !covered[effort] {
			report.Issues = append(report.Issues, verificationIssue(compiled, EncodeRequest, "/agent/samples", IncompleteCoverage, "Agent lacks passing evidence for thinking effort: "+effort, ""))
		}
	}
	for _, operation := range compiled.Operations() {
		if operation.Kind == "generate" && (operation.Transport == SSE || operation.Transport == NDJSON) && !hasStream {
			report.Issues = append(report.Issues, verificationIssue(compiled, EncodeRequest, "/agent/samples", IncompleteCoverage, "streaming Agent use requires a passing streaming preferences fixture", ""))
			break
		}
	}
}

func verifyAgentParameterWire(ctx context.Context, compiled *Compiled, parameters Object) error {
	for _, sample := range compiled.Definition().Samples {
		if sample.Direction != DecodeRequest || sample.ExpectedIssue != "" {
			continue
		}
		options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
		request, err := compiled.DecodeRequest(ctx, sample.Input.Bytes(), options)
		if err != nil {
			return err
		}
		request.Native, request.Parameters = nil, parameters
		encoded, err := compiled.EncodeRequest(ctx, request, options)
		if err != nil {
			return err
		}
		decoded, err := compiled.DecodeRequest(ctx, encoded, options)
		if err != nil {
			return err
		}
		before, _ := EncodeValue(parameters)
		after, _ := EncodeValue(decoded.Parameters)
		if !equalValues(before, after) {
			return fmt.Errorf("Agent parameters are lost or changed by the wire adapter")
		}
		return verifyAgentToolResult(ctx, compiled, decoded.Model, parameters, options)
	}
	return fmt.Errorf("Agent policy requires a positive request fixture")
}

func verifyAgentToolResult(ctx context.Context, compiled *Compiled, model Value, parameters Object, options EvaluationContext) error {
	arguments, _ := ParseValue([]byte(`{"value":9007199254740993}`))
	payload, _ := ParseValue([]byte(`{"ok":false,"data":{"value":9007199254740993,"empty":null}}`))
	if compiled.Definition().Agent.ToolResults == TextInput {
		payload = StringValue(string(payload.Bytes()))
	}
	schema, _ := ParseValue([]byte(`{"type":"object"}`))
	request := &Request{SchemaVersion: SemanticSchemaVersion, Model: model, Parameters: parameters,
		Tools: []Tool{{Kind: FunctionTool, Name: StringValue("agent_verify"), InputSchema: schema}},
		Content: []Node{
			{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("verify Agent tool history")}}},
			{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: ToolCallNode, Name: StringValue("agent_verify"), CallID: StringValue("agent_verify_call"), Input: &ToolInput{Kind: JSONInput, Value: arguments}}}},
			{Kind: ToolResultNode, Name: StringValue("agent_verify"), CallID: StringValue("agent_verify_call"), Payload: payload},
		}}
	wire, err := compiled.EncodeRequest(ctx, request, options)
	if err != nil {
		return err
	}
	decoded, err := compiled.DecodeRequest(ctx, wire, options)
	if err != nil {
		return err
	}
	var calls, results []Node
	var visit func([]Node)
	visit = func(nodes []Node) {
		for _, node := range nodes {
			if node.Kind == ToolCallNode {
				calls = append(calls, node)
			}
			if node.Kind == ToolResultNode {
				results = append(results, node)
			}
			visit(node.Children)
		}
	}
	visit(decoded.Content)
	if len(calls) != 1 || len(results) != 1 || calls[0].CallID != StringValue("agent_verify_call") || results[0].CallID != calls[0].CallID || calls[0].Name != StringValue("agent_verify") || calls[0].Input == nil || !equalValues(calls[0].Input.Value, arguments) || !equalValues(results[0].Payload, payload) {
		return fmt.Errorf("Agent tool history loses its arguments, result payload or call association")
	}
	return nil
}
