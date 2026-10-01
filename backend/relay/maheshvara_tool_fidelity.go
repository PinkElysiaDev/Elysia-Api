package relay

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

const legacyWireContractVersion = "legacy-v1"

func toolNativeTarget(format FormatType) protocol.Target {
	return protocol.Target{Protocol: protocol.Identity{Family: string(format), WireVersion: legacyWireContractVersion}, Direction: protocol.EncodeRequest}
}

// Native tool schemas are wire-specific, even when vendors reuse a type name.
func renderNativeTool(tool MaheshvaraTool, target FormatType) (map[string]any, error) {
	if tool.Raw == nil {
		return nil, fmt.Errorf("unsupported_tool: %q has no native definition", tool.Type)
	}
	if strings.TrimSpace(tool.Type) == "" {
		return nil, fmt.Errorf("invalid_tool: tools[].type is required")
	}
	if target == FormatResponses && tool.Type == "custom" && strings.TrimSpace(tool.Name) == "" {
		return nil, fmt.Errorf("invalid_tool: tools[].name is required for custom tools")
	}
	nativeValue, err := protocol.EncodeValue(tool.Raw)
	if err != nil {
		return nil, err
	}
	native := protocol.Native{Source: protocol.Provenance{Protocol: toolNativeTarget(tool.sourceFormat).Protocol, Direction: protocol.DecodeRequest, Path: "/tools"}, Value: nativeValue}
	var mutations []protocol.Mutation
	if tool.Name != stringValue(tool.Raw["name"]) {
		mutations = append(mutations, protocol.Mutation{Op: protocol.SetValue, Path: "/name", Value: protocol.StringValue(tool.Name)})
	}
	preserved, issues := protocol.PreserveNative(native, toolNativeTarget(target), mutations, protocol.DefaultLimits())
	if err := protocol.IssuesError(issues); err != nil {
		return nil, err
	}
	var output map[string]any
	if err := preserved.Decode(&output); err != nil {
		return nil, err
	}
	return output, nil
}

func renderFunctionTool(tool MaheshvaraTool, target FormatType, schemaKey string) (map[string]any, error) {
	if strings.TrimSpace(tool.Name) == "" {
		return nil, fmt.Errorf("invalid_tool: tools[].name is required for function tools")
	}
	output := make(map[string]any)
	if tool.sourceFormat == target {
		raw := tool.Raw
		if target == FormatOpenAIChat {
			raw = mapValue(raw["function"])
		}
		maps.Copy(output, raw)
	}
	output["name"] = tool.Name
	if tool.Description != "" || output["description"] != nil {
		output["description"] = tool.Description
	}
	if schema := firstNonNilMap(tool.Parameters, tool.InputSchema); schema != nil {
		output[schemaKey] = schema
	} else {
		delete(output, schemaKey)
	}
	if tool.Strict != nil {
		output["strict"] = *tool.Strict
	} else {
		delete(output, "strict")
	}
	return output, nil
}

func validateToolDefinitions(tools []MaheshvaraTool) error {
	for index, tool := range tools {
		if tool.Type == MaheshvaraToolFunction && strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("invalid_tool: tools[%d].name is required", index)
		}
	}
	return nil
}

func validateToolHistory(req *MaheshvaraRequest, target FormatType) error {
	if err := validateToolDefinitions(req.Tools); err != nil {
		return err
	}
	if target == FormatResponses {
		return nil
	}
	for index, item := range req.InputItems {
		switch item.Type {
		case MaheshvaraInputMessage, MaheshvaraInputFunctionCallOutput, MaheshvaraOutputFunctionCall, MaheshvaraOutputReasoning:
		default:
			return fmt.Errorf("unsupported_history: input[%d] type %q has no equivalent in %s", index, item.Type, target)
		}
	}
	return nil
}

func validateToolResponse(resp *MaheshvaraResponse, target FormatType) error {
	if resp == nil {
		return fmt.Errorf("nil Maheshvara response")
	}
	for index, item := range resp.Output {
		if item.Type == MaheshvaraOutputFunctionCall && !json.Valid(item.Arguments) {
			return fmt.Errorf("invalid_tool_input: output[%d].arguments is missing or invalid JSON", index)
		}
		if err := validateToolOutput(item, target); err != nil {
			return fmt.Errorf("output[%d]: %w", index, err)
		}
	}
	return nil
}

func validateToolOutput(item MaheshvaraOutputItem, target FormatType) error {
	if item.Type == "custom_tool_call" && target != FormatResponses {
		return fmt.Errorf("unsupported_tool_output: free text tool input has no declared equivalent in %s", target)
	}
	if item.sourceFormat != FormatResponses || target == FormatResponses {
		return nil
	}
	switch item.Type {
	case MaheshvaraOutputMessage, MaheshvaraOutputFunctionCall, MaheshvaraOutputReasoning:
		return nil
	default:
		return fmt.Errorf("unsupported_tool_output: %q has no equivalent in %s", item.Type, target)
	}
}

func validateToolEvent(event *MaheshvaraStreamEvent, target FormatType) error {
	if event.OutputItem != nil {
		if err := validateToolOutput(*event.OutputItem, target); err != nil {
			return err
		}
	}
	if event.Response != nil {
		if err := validateToolResponse(event.Response, target); err != nil {
			return err
		}
	}
	if event.sourceFormat != FormatResponses || target == FormatResponses {
		return nil
	}
	switch event.Type {
	case MaheshvaraEventResponseCreated, MaheshvaraEventResponseInProgress,
		MaheshvaraEventOutputItemAdded, MaheshvaraEventOutputItemDone,
		MaheshvaraEventContentPartAdded, MaheshvaraEventContentPartDone,
		MaheshvaraEventTextDelta, MaheshvaraEventTextDone,
		MaheshvaraEventRefusalDelta, MaheshvaraEventRefusalDone,
		MaheshvaraEventReasoningDelta, MaheshvaraEventReasoningDone,
		MaheshvaraEventReasoningSummaryDelta, MaheshvaraEventReasoningSummaryDone,
		MaheshvaraEventReasoningSignatureDelta,
		MaheshvaraEventFunctionCallArgumentsDelta, MaheshvaraEventFunctionCallArgumentsDone,
		MaheshvaraEventUsageDelta, MaheshvaraEventResponseCompleted, MaheshvaraEventResponseFailed:
		return nil
	default:
		return fmt.Errorf("unsupported_event: %q has no equivalent in %s", event.Type, target)
	}
}

// Legacy stream templates can express JSON function arguments only. Blocking
// known incompatible payloads prevents frame filters from hiding tool calls.
func validateLegacyToolFrame(frame map[string]any) error {
	if strings.Contains(stringValue(frame["type"]), ".custom_tool_call") {
		return fmt.Errorf("unsupported_event: free text tool streams require a native event adapter")
	}
	items := []any{frame["item"]}
	if response := mapValue(frame["response"]); response != nil {
		if output, ok := response["output"].([]any); ok {
			items = append(items, output...)
		}
	}
	for _, value := range items {
		typeName := stringValue(mapValue(value)["type"])
		if strings.HasSuffix(typeName, "_call") && typeName != "function_call" {
			return fmt.Errorf("unsupported_event: tool type %q requires a native event adapter", typeName)
		}
	}
	return nil
}

func maheshvaraToolsToOpenAI(tools []MaheshvaraTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			native, err := renderNativeTool(tool, FormatOpenAIChat)
			if err != nil {
				return nil, err
			}
			out = append(out, native)
			continue
		}
		if isLegacyFunctionTool(tool) {
			// 遗留工具由调用方按 functions 形态分流，此处跳过。
			continue
		}
		function, err := renderFunctionTool(tool, FormatOpenAIChat, "parameters")
		if err != nil {
			return nil, err
		}
		out = append(out, withCacheControl(map[string]any{
			"type":     "function",
			"function": function,
		}, tool.CacheControl))
	}
	return out, nil
}

// legacyFunctionCallIDPrefix 是遗留 function calling 的调用 ID 前缀：
// role:"function" 结果消息没有 tool_call_id，用该前缀 + 函数名合成，
// 与 assistant function_call 的 ID 对齐。
const legacyFunctionCallIDPrefix = "legacy_function:"

func isLegacyFunctionTool(tool MaheshvaraTool) bool {
	return tool.Raw != nil && tool.Raw["legacy_function"] == true
}

func isLegacyFunctionCall(call MaheshvaraToolCall) bool {
	// 只认解析器打的显式标记，不按 ID 前缀猜测——真实工具调用的 id 可能
	// 恰好以 "legacy_function:" 开头（客户端可造），前缀猜测会把它错误地
	// 降级成旧形态。
	return call.Raw != nil && call.Raw["legacy_function"] == true
}

func maheshvaraToolsToClaude(tools []MaheshvaraTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			native, err := renderNativeTool(tool, FormatClaude)
			if err != nil {
				return nil, err
			}
			out = append(out, native)
			continue
		}
		item, err := renderFunctionTool(tool, FormatClaude, "input_schema")
		if err != nil {
			return nil, err
		}
		if tool.CacheControl != nil {
			item["cache_control"] = tool.CacheControl
		}
		out = append(out, item)
	}
	return out, nil
}

func maheshvaraToolsToGemini(tools []MaheshvaraTool) ([]map[string]any, error) {
	var declarations []map[string]any
	var nativeTools []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			native, err := renderNativeTool(tool, FormatGemini)
			if err != nil {
				return nil, err
			}
			nativeTools = append(nativeTools, native)
			continue
		}
		declaration, err := renderFunctionTool(tool, FormatGemini, "parameters")
		if err != nil {
			return nil, err
		}
		declarations = append(declarations, declaration)
	}
	var out []map[string]any
	if len(declarations) > 0 {
		out = append(out, map[string]any{"functionDeclarations": declarations})
	}
	out = append(out, nativeTools...)
	return out, nil
}

func maheshvaraToolsToResponses(tools []MaheshvaraTool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		var rendered map[string]any
		var err error
		if tool.Type == MaheshvaraToolFunction {
			rendered, err = renderFunctionTool(tool, FormatResponses, "parameters")
		} else {
			rendered, err = renderNativeTool(tool, FormatResponses)
		}
		if err != nil {
			return nil, err
		}
		rendered["type"] = tool.Type
		out = append(out, rendered)
	}
	return out, nil
}
