package server

import (
	"context"
	"encoding/json"
	"time"
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
}

func CLIError(summary, code string) CLIResult {
	return CLIResult{OK: false, Summary: summary, Data: map[string]any{"error": code}}
}

func (r CLIResult) MarshalData() json.RawMessage {
	if r.Data == nil {
		return json.RawMessage(`{}`)
	}
	if raw, ok := r.Data.(json.RawMessage); ok {
		return raw
	}
	encoded, err := json.Marshal(r.Data)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
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
