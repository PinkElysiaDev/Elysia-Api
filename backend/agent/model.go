package agent

import (
	"context"

	"github.com/elysia-api/backend/relay"
)

// StreamCaller 是引擎对「一次流式模型调用」的抽象。实现负责线格式渲染、
// SSE 解码、传输层重试；引擎只消费聚合结果与增量回调。
type StreamCaller interface {
	// Call 发起一次流式调用。回调在 Call 返回前同步发出（含并发安全由实现
	// 保证）；返回值为聚合后的完整结果。
	Call(ctx context.Context, req CallRequest, cb StreamCallbacks) (*CallResult, error)
}

// CallRequest 一次模型调用的全部输入。
type CallRequest struct {
	Model           string
	ModelSourceID   string // 模型源 id（实现层解析端点/凭据）
	Instructions    string // 系统提示词
	Messages        []relay.MaheshvaraMessage
	Tools           []relay.MaheshvaraTool
	Thinking        *relay.MaheshvaraThinking
	Reasoning       *relay.MaheshvaraReasoning // OpenAI 线 reasoning_effort 渲染路径
	MaxOutputTokens int
}

// StreamCallbacks 增量回调（均可为 nil）。
type StreamCallbacks struct {
	OnText      func(delta string)
	OnReasoning func(delta string)
}

// CallResult 一次调用的聚合产出。
type CallResult struct {
	Text         string
	Reasoning    string
	ToolCalls    []relay.MaheshvaraToolCall
	Usage        *relay.MaheshvaraUsage
	FinishReason string
}
