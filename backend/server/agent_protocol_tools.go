package server

import "strings"

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// 协议工具共享的取值与提示文案。
const missingTestTargetSummary = "测试目标 baseUrl 未配置：请向用户询问上游 baseUrl（以及需要鉴权时的 API key）；用户在对话中给出后作为本工具参数传入"

func resolveTestTarget(tctx CLIContext, paramBaseURL, paramAPIKey string) (baseURL, apiKey string, failure CLIResult, ok bool) {
	baseURL, apiKey = tctx.TestTarget()
	if strings.TrimSpace(paramBaseURL) != "" {
		baseURL = strings.TrimSpace(paramBaseURL)
	}
	if strings.TrimSpace(paramAPIKey) != "" {
		apiKey = paramAPIKey
	}
	if strings.TrimSpace(baseURL) == "" {
		return "", "", CLIResult{OK: false, Summary: missingTestTargetSummary, Data: map[string]any{"error": "missing_test_target"}}, false
	}
	if err := tctx.SetTestTarget(baseURL, apiKey); err != nil {
		return "", "", CLIError("Cannot retain test credentials", err.Error()), false
	}
	return baseURL, apiKey, CLIResult{}, true
}
