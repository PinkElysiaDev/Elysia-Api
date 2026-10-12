package agent

import "encoding/json"

const invalidToolResultSummary = "工具返回的数据不是合法 JSON，未重试已执行的工具"

func rejectToolResult(info ToolResultInfo) ToolResultInfo {
	info.OK = false
	info.Summary = invalidToolResultSummary
	info.Data = json.RawMessage(`{"error":"invalid_tool_result"}`)
	info.SecretValues = nil
	return info
}

func validateToolResult(info ToolResultInfo) ToolResultInfo {
	if len(info.Data) > 0 && !json.Valid(info.Data) {
		return rejectToolResult(info)
	}
	return info
}

// redactToolResult runs before truncation so a cut through a secret cannot
// defeat exact-value masking. This copy is shared by storage and model calls.
func redactToolResult(info ToolResultInfo) ToolResultInfo {
	info.Input = MaskSecretInputs(info.Input)
	if len(info.SecretValues) > 0 {
		info.Summary = MaskSecretText(info.Summary, info.SecretValues)
		info.Data = MaskSecretData(info.Data, info.SecretValues)
	}
	info.SecretValues = nil
	return info
}

func limitToolResult(info ToolResultInfo, limit int, direction clampDirection) ToolResultInfo {
	info.Summary = clampSummary(info.Summary, summaryModelLimit)
	info.Data = clampJSON(info.Data, limit, direction)
	return info
}
