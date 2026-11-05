package agent

import (
	"sync"
	"time"
)

// Engine 是领域无关的 Agent 执行引擎：模型流式调用 → 工具分发 → 门控暂停/
// 审批恢复，全部状态经 Store 持久化，事件经 channel 流出（SSE 编码在宿主）。
//
// 生命周期：一个会话同一时刻至多一个活动轮次（内存锁强制）；轮次脱离 HTTP
// 请求上下文运行——浏览器断开后轮次继续，结果落库，回来即可见；Stop 是唯一
// 的中途取消途径（另有 TurnTimeout 兜底）。
type Engine struct {
	caller StreamCaller
	store  Store
	tools  *Registry
	render UserContentRenderer
	prompt func(*Session) string
	opts   Options

	mu       sync.Mutex
	running  map[string]*turnHandle
	isClosed bool
}

// Options 引擎行为参数（零值字段取默认）。
type Options struct {
	MaxModelCalls int // 单轮最大模型调用次数（工具循环上限）
	// ModelCallLimit 为运行时动态上限（热重载）；非 nil 且返回 >0 时优先生效。
	ModelCallLimit func() int
	TurnTimeout    time.Duration // 单轮总超时
	EventBuffer    int           // 事件 channel 缓冲
	// ToolResultModelLimit 回传模型的工具结果字节上限（超出截断）。
	ToolResultModelLimit int
	// ToolResultStoreLimit 持久化的工具结果字节上限（超出截断）。
	ToolResultStoreLimit int
	// ContextWindowTokens 上下文窗口；0 取默认 128k。
	ContextWindowTokens int
	// ParseAsk 把 ask_user 的调用参数解析为问题；宿主在装配时注入（引擎
	// 不认识工具参数形状）。nil 时 ask_user 按普通工具执行并返回错误。
	ParseAsk func(call FunctionCall) (AskQuestion, bool)
}

// DefaultMaxModelCalls 是单轮工具循环的默认模型调用上限。
const DefaultMaxModelCalls = 30

func (o Options) withDefaults() Options {
	if o.MaxModelCalls <= 0 {
		o.MaxModelCalls = DefaultMaxModelCalls
	}
	if o.TurnTimeout <= 0 {
		o.TurnTimeout = 5 * time.Minute
	}
	if o.EventBuffer <= 0 {
		o.EventBuffer = 256
	}
	if o.ToolResultModelLimit <= 0 {
		o.ToolResultModelLimit = 16 << 10
	}
	if o.ToolResultStoreLimit <= 0 {
		o.ToolResultStoreLimit = 64 << 10
	}
	if o.ContextWindowTokens <= 0 {
		o.ContextWindowTokens = defaultContextWindow
	}
	if o.ParseAsk == nil {
		o.ParseAsk = func(FunctionCall) (AskQuestion, bool) { return AskQuestion{}, false }
	}
	return o
}

// NewEngine 装配引擎。systemPrompt 由宿主提供（领域知识），可为 nil
// （无系统提示词）。
func NewEngine(caller StreamCaller, store Store, tools *Registry, render UserContentRenderer, systemPrompt func(*Session) string, opts Options) *Engine {
	if render == nil {
		render = TextUserContentRenderer{}
	}
	return &Engine{
		caller:  caller,
		store:   store,
		tools:   tools,
		render:  render,
		prompt:  systemPrompt,
		opts:    opts.withDefaults(),
		running: make(map[string]*turnHandle),
	}
}

// IsRunning 报告会话是否有活动轮次。
func (e *Engine) IsRunning(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, running := e.running[sessionID]
	return running
}
