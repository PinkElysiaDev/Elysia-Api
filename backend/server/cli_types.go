package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
)

// CLIContext 是 Elysia CLI 在一次执行中的最小状态接口。它不包含 Agent
// 会话、审批或计划概念；宿主可以把它实现为会话适配器，也可以实现为一次
// 调用范围内的临时内存状态。
type CLIContext interface {
	Draft() json.RawMessage
	SetDraft(json.RawMessage) error
	TestTarget() (baseURL, apiKey string)
	SetTestTarget(baseURL, apiKey string) error
	EditProtocolID() string
	ReportProgress(text string)
}

// CLIResult 是 CLI handler 的中立结果类型。Agent/MCP 适配器在边界处再把
// 它转换成各自的结果结构。
type CLIResult struct {
	OK      bool
	Summary string
	Data    any
	// SecretValues 声明结果里出现的明文密钥（如 key create 生成的 token）：
	// 现场事件保留原值交付一次，落库/回放出口按值精确打码（见 agent.ToolResult）。
	SecretValues []string
	RedactedData json.RawMessage
}

func CLIError(summary, code string) CLIResult {
	return CLIResult{OK: false, Summary: summary, Data: map[string]any{"error": code}}
}

// MarshalData rejects invalid tool data without inventing an empty success.
func (r CLIResult) MarshalData() (json.RawMessage, error) {
	if r.Data == nil {
		return json.RawMessage(`{}`), nil
	}
	return sonic.ConfigStd.Marshal(r.Data)
}

func normalizeCLIResult(result CLIResult) CLIResult {
	if result.Data == nil {
		return result
	}
	encoded, err := result.MarshalData()
	if err != nil {
		return CLIResult{OK: false, Summary: "工具返回的数据不是合法 JSON，未重试已执行的命令", Data: json.RawMessage(`{"error":"invalid_tool_result"}`)}
	}
	result.Data = encoded
	return result
}

// CLIHandler 是命令业务实现的唯一执行契约。它不暴露模型工具、审批或
// Agent 会话接口。
type CLIHandler interface {
	Execute(context.Context, CLIContext, json.RawMessage) CLIResult
}

// CLIHandlerMeta 是 handler 的可选执行超时配置。
type CLIHandlerMeta interface {
	CLIMeta() CLICommandMeta
}

type CLIHandlerEffect interface {
	CLIEffect() string
}

type CLICommandMeta struct {
	Timeout time.Duration
}

const (
	CLIEffectRead     = "read"
	CLIEffectWrite    = "write"
	CLIEffectOutbound = "outbound"
	CLIEffectDelete   = "delete"
	cliDefaultTimeout = 120 * time.Second
	cliBatchTimeout   = 10 * time.Minute
)

func cliMetaOf(handler CLIHandler) CLICommandMeta {
	if withMeta, ok := handler.(CLIHandlerMeta); ok {
		meta := withMeta.CLIMeta()
		if meta.Timeout <= 0 {
			meta.Timeout = cliDefaultTimeout
		}
		return meta
	}
	return CLICommandMeta{Timeout: cliDefaultTimeout}
}

func cliEffectOf(handler CLIHandler) string {
	if withEffect, ok := handler.(CLIHandlerEffect); ok {
		if effect := withEffect.CLIEffect(); effect != "" {
			return effect
		}
	}
	return CLIEffectRead
}

func cliDescriptionOf(handler CLIHandler) string {
	if described, ok := handler.(interface{ Description() string }); ok {
		return described.Description()
	}
	return ""
}

func cliHandlerName(handler CLIHandler) string {
	if named, ok := handler.(interface{ Name() string }); ok {
		return named.Name()
	}
	return ""
}
