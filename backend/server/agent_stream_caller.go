package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 协议 Agent 的流式模型客户端（agent.StreamCaller）与用户内容渲染器。
// 全部复用 relay 的线格式渲染与 Maheshvara 流解码器——与线上中转同一套
// 转换代码；传输层带首事件前重试（网络错/429/5xx）。

const (
	agentStreamMaxOutputTokens = 8192 // 单次模型调用输出上限（工具调用参数也占这里）
	agentStreamMaxRetries      = 2    // 首事件前重试次数
	agentStreamErrorBodyLimit  = 8 << 10
	agentCallTimeoutSec        = 300 // 单次模型调用硬性上限（轮次总超时另由引擎控制）

	// 用户附件限额（与一代助手一致）。
	agentDocMax      = 20
	agentDocMaxText  = 512 << 10
	agentDocMaxFile  = 8 << 20
	agentDocMaxTotal = 32 << 20
)

// agentStreamCaller 实现 agent.StreamCaller：按会话设置的模型源/模型解析
// 端点与凭据，流式调用并聚合增量。
type agentStreamCaller struct {
	server *Server
}

func newAgentStreamCaller(s *Server) *agentStreamCaller {
	return &agentStreamCaller{server: s}
}

// agentStreamAccumulator 聚合一次流式调用的增量。
type agentStreamAccumulator struct {
	text      strings.Builder
	reasoning strings.Builder
	usage     *relay.MaheshvaraUsage
	finish    string
	failure   string

	tools     map[string]*agentToolCallState
	toolOrder []string
}

type agentToolCallState struct {
	id        string
	name      string
	arguments strings.Builder
}

func (a *agentStreamAccumulator) toolState(key string) *agentToolCallState {
	if state, ok := a.tools[key]; ok {
		return state
	}
	state := &agentToolCallState{}
	a.tools[key] = state
	a.toolOrder = append(a.toolOrder, key)
	return state
}

func (a *agentStreamAccumulator) keyOf(event relay.MaheshvaraStreamEvent) string {
	if event.ToolCallID != "" {
		return "id:" + event.ToolCallID
	}
	return fmt.Sprintf("idx:%d", event.ToolCallIndex)
}

func (a *agentStreamAccumulator) toolCalls() []relay.MaheshvaraToolCall {
	if len(a.toolOrder) == 0 {
		return nil
	}
	calls := make([]relay.MaheshvaraToolCall, 0, len(a.toolOrder))
	for _, key := range a.toolOrder {
		state := a.tools[key]
		if state.name == "" && state.arguments.Len() == 0 {
			continue
		}
		arguments := state.arguments.String()
		if !json.Valid([]byte(arguments)) {
			arguments = "{}"
		}
		calls = append(calls, relay.MaheshvaraToolCall{
			ID: state.id, Type: "function", Name: state.name,
			Arguments: json.RawMessage(arguments),
		})
	}
	return calls
}

// Call 实现 agent.StreamCaller。取消路径下返回部分聚合结果 + ctx 错误。
func (c *agentStreamCaller) Call(ctx context.Context, req agent.CallRequest, cb agent.StreamCallbacks) (*agent.CallResult, error) {
	store := c.server.store
	if store == nil {
		return nil, fmt.Errorf("sqlite store is unavailable")
	}
	model, found := findCustomProtocolTestModel(ctx, store, req.ModelSourceID, req.Model)
	if !found {
		return nil, fmt.Errorf("模型源 %q 下没有找到模型 %q", req.ModelSourceID, req.Model)
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = agentStreamMaxOutputTokens
	}
	maheshvara := &relay.MaheshvaraRequest{
		Model:           model.Name,
		Instructions:    req.Instructions,
		Messages:        req.Messages,
		Tools:           req.Tools,
		Thinking:        req.Thinking,
		Reasoning:       req.Reasoning,
		Stream:          true,
		MaxOutputTokens: maxTokens,
	}
	plan, err := renderAgentUpstreamPlan(maheshvara, model.Platform)
	if err != nil {
		return nil, err
	}

	timeout := c.server.probeTimeout(agentCallTimeoutSec * time.Second)
	backoffs := []time.Duration{time.Second, 3 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= agentStreamMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoffs[attempt-1]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		result, retryable, err := c.callOnce(callCtx, cancel, model, plan, cb)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			return result, err
		}
	}
	return nil, fmt.Errorf("模型调用失败（已重试 %d 次）: %w", agentStreamMaxRetries, lastErr)
}

// agentUpstreamPlan 一次模型调用的发送计划：线格式请求体 + 平台路由。
type agentUpstreamPlan struct {
	format      string // NormalizeAPIFormat 结果；custom:<id> 走自定义协议分支
	body        []byte // 线格式请求体（OpenAI 系已注入 stream/include_usage）
	customReq   *relay.CustomProtocolRequestResult
	protocolID  string // custom 分支的注册协议 ID
}

// renderAgentUpstreamPlan 渲染请求体并决定发送路由，全部复用 relay 转换内核
// （MahshvaraToTargetRequest 的同套分发 + 转发路径的 stream 注入语义）。
// custom:<id> 平台走注册协议渲染——修复了旧实现把自定义协议源错按 OpenAI
// 线制发送的问题。
func renderAgentUpstreamPlan(request *relay.MaheshvaraRequest, platform string) (*agentUpstreamPlan, error) {
	format := relay.NormalizeAPIFormat(platform)
	if strings.HasPrefix(format, "custom:") {
		protocolID := strings.TrimPrefix(format, "custom:")
		if _, ok := relay.GetCustomProtocol(protocolID); !ok {
			return nil, fmt.Errorf("自定义协议 %q 未注册", protocolID)
		}
		rendered, err := relay.RenderRegisteredCustomProtocolRequest(request, protocolID)
		if err != nil {
			return nil, fmt.Errorf("构建自定义协议请求失败: %w", err)
		}
		return &agentUpstreamPlan{format: format, protocolID: protocolID, customReq: rendered}, nil
	}

	var body []byte
	var err error
	switch format {
	case relay.APIFormatAnthropic:
		body, err = relay.MaheshvaraToAnthropic(request)
	case relay.APIFormatGemini:
		body, err = relay.MaheshvaraToGemini(request)
	case relay.APIFormatResponses:
		body, err = relay.MaheshvaraToOpenAIResponses(request, nil)
	default:
		body, err = relay.MaheshvaraToOpenAIChat(request)
	}
	if err != nil {
		return nil, fmt.Errorf("构建模型请求失败: %w", err)
	}
	// stream 标志注入与转发热路径（ensureStreamFlagInTargetBody）同源：Gemini
	// 经 URL action 决定流式不注入；OpenAI 系补 stream_options.include_usage
	// 让上游回 usage 帧（agent 的 token 统计依赖它）。
	switch format {
	case relay.APIFormatGemini:
	case relay.APIFormatAnthropic:
		body, err = relay.PassthroughBody(body, "", true, false)
	default:
		body, err = relay.PassthroughBody(body, "", true, true)
	}
	if err != nil {
		return nil, fmt.Errorf("注入流式标志失败: %w", err)
	}
	return &agentUpstreamPlan{format: format, body: body}, nil
}

// sendAgentUpstream 按平台把请求交给 relay 适配器发送——端点拼接、鉴权头、
// HTTP 客户端（共享连接池 + 动态超时 + 安全传输）与线上转发是同一实现，
// 不再由 agent 侧自行拼 URL/设头/建客户端。流式返回原始响应（调用方负责
// 关闭）；非 2xx 统一收敛为 *relay.UpstreamStatusError。
func (c *agentStreamCaller) sendAgentUpstream(ctx context.Context, model storage.Model, plan *agentUpstreamPlan) (*http.Response, error) {
	if plan.customReq != nil {
		response, err := c.server.openaiAdapter.SendCustomProtocolRequest(ctx, model.BaseURL, model.APIKey, plan.customReq, true)
		return agentNormalizeUpstreamResponse(response, err)
	}
	switch plan.format {
	case relay.APIFormatAnthropic:
		response, err := c.server.claudeAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, plan.body, true)
		return agentNormalizeUpstreamResponse(response, err)
	case relay.APIFormatGemini:
		response, err := c.server.geminiAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, model.Name, plan.body, true)
		return agentNormalizeUpstreamResponse(response, err)
	case relay.APIFormatResponses:
		// OpenAI 适配器的流式入口自带非 2xx → UpstreamStatusError 收敛。
		return c.server.openaiAdapter.SendResponsesStream(ctx, model.BaseURL, model.APIKey, plan.body)
	default:
		return c.server.openaiAdapter.SendRequestStream(ctx, model.BaseURL, model.APIKey, plan.body)
	}
}

// agentNormalizeUpstreamResponse 把「返回原始响应」的适配器（Claude/Gemini/
// 自定义协议）的非 2xx 情况收敛成与 OpenAI 适配器一致的 UpstreamStatusError，
// 供 callOnce 统一做重试分类与错误文案。
func agentNormalizeUpstreamResponse(response *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, agentStreamErrorBodyLimit))
	return nil, &relay.UpstreamStatusError{StatusCode: response.StatusCode, Body: string(raw)}
}

// agentStreamDecoderFormat 把平台归一化格式映射为流解码器的 FormatType。
// 两套常量字面量并不一致（APIFormatAnthropic="anthropic" vs FormatClaude=
// "claude"），直接裸转会静默落进 OpenAI chat 解码器——旧实现即因此让
// Anthropic/Gemini 上游的流被错误解码。
func agentStreamDecoderFormat(format string) relay.FormatType {
	switch format {
	case relay.APIFormatAnthropic:
		return relay.FormatClaude
	case relay.APIFormatGemini:
		return relay.FormatGemini
	case relay.APIFormatResponses:
		return relay.FormatResponses
	default:
		return relay.FormatOpenAIChat
	}
}

// callOnce 发起一次流式调用。retryable 表示失败发生在收到任何流事件之前
// 且状态值得重试（网络错/429/5xx）。
func (c *agentStreamCaller) callOnce(ctx context.Context, cancel context.CancelFunc, model storage.Model, plan *agentUpstreamPlan, cb agent.StreamCallbacks) (result *agent.CallResult, retryable bool, err error) {
	defer cancel()
	response, err := c.sendAgentUpstream(ctx, model, plan)
	if err != nil {
		var statusErr *relay.UpstreamStatusError
		if errors.As(err, &statusErr) {
			retryable = statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
			return nil, retryable, fmt.Errorf("上游模型返回 %d: %s", statusErr.StatusCode, truncateForDisplay(statusErr.Body, 2048))
		}
		return nil, true, err
	}
	defer response.Body.Close()

	acc := &agentStreamAccumulator{tools: map[string]*agentToolCallState{}}
	if plan.customReq != nil {
		if streamErr := c.drainCustomProtocolStream(ctx, plan, response.Body, acc, cb); streamErr != nil {
			// 流中途故障：带部分结果返回（不重试，避免重复下发增量）。
			return acc.result(), false, streamErr
		}
	} else {
		reader := relay.NewSSEEventReader(response.Body)
		defer reader.Close()
		decoder := relay.NewMaheshvaraStreamDecoder(agentStreamDecoderFormat(plan.format))
		for {
			event, ok, readErr := reader.Read(ctx, relay.DefaultSSEIdleTimeout)
			if readErr != nil {
				// 流中途故障：带部分结果返回（不重试，避免重复下发增量）。
				return acc.result(), false, readErr
			}
			if !ok {
				break
			}
			events, decodeErr := decoder.Decode(event)
			if decodeErr != nil {
				log.Printf("[agent-stream] decode error: %v (data=%.200s)", decodeErr, event.Data)
				continue // 单事件解码失败容忍（与转发路径一致）
			}
			for _, ev := range events {
				if stop := acc.apply(ev, cb); stop {
					return acc.result(), false, nil
				}
			}
		}
	}
	if ctx.Err() != nil {
		return acc.result(), false, ctx.Err()
	}
	if failed := acc.failure; failed != "" {
		return acc.result(), false, fmt.Errorf("%s", failed)
	}
	return acc.result(), false, nil
}

// drainCustomProtocolStream 用注册协议的流解码器排水 SSE（与自定义协议转发
// 路径 ForEachBatch 同一套终态/排水语义），把每批事件喂给聚合器。
func (c *agentStreamCaller) drainCustomProtocolStream(ctx context.Context, plan *agentUpstreamPlan, body io.Reader, acc *agentStreamAccumulator, cb agent.StreamCallbacks) error {
	protocol, ok := relay.GetCustomProtocol(plan.protocolID)
	if !ok {
		return fmt.Errorf("自定义协议 %q 未注册", plan.protocolID)
	}
	decoder, err := relay.NewRegisteredCustomProtocolStreamDecoder(protocol)
	if err != nil {
		return fmt.Errorf("构造流解码器失败: %w", err)
	}
	reader := relay.NewSSEEventReader(body)
	defer reader.Close()
	return decoder.ForEachBatch(ctx, reader, func(_ relay.SSEEvent, events []relay.MaheshvaraStreamEvent, _ bool) error {
		for _, ev := range events {
			acc.apply(ev, cb) // 终态语义由 ForEachBatch 管理，聚合器无需中断
		}
		return nil
	})
}

// apply 归并单个流事件；返回 true 表示终态已到，可停止读取。
func (a *agentStreamAccumulator) apply(event relay.MaheshvaraStreamEvent, cb agent.StreamCallbacks) bool {
	switch event.Type {
	case relay.MaheshvaraEventTextDelta:
		if event.Delta != "" {
			a.text.WriteString(event.Delta)
			if cb.OnText != nil {
				cb.OnText(event.Delta)
			}
		}
	case relay.MaheshvaraEventReasoningDelta, relay.MaheshvaraEventReasoningSummaryDelta:
		if event.ReasoningDelta != "" {
			a.reasoning.WriteString(event.ReasoningDelta)
			if cb.OnReasoning != nil {
				cb.OnReasoning(event.ReasoningDelta)
			}
		}
	case relay.MaheshvaraEventFunctionCallAdded:
		state := a.toolState(a.keyOf(event))
		if event.ToolCallID != "" {
			state.id = event.ToolCallID
		}
		if event.ToolName != "" {
			state.name = event.ToolName
		}
	case relay.MaheshvaraEventFunctionCallArgumentsDelta:
		state := a.toolState(a.keyOf(event))
		if event.ToolCallID != "" {
			state.id = event.ToolCallID
		}
		if event.ToolName != "" {
			state.name = event.ToolName
		}
		state.arguments.WriteString(event.ToolArgumentsDelta)
	case relay.MaheshvaraEventFunctionCallArgumentsDone:
		state := a.toolState(a.keyOf(event))
		if event.ToolCallID != "" {
			state.id = event.ToolCallID
		}
		if event.ToolName != "" {
			state.name = event.ToolName
		}
		state.arguments.Reset()
		state.arguments.WriteString(event.ToolArgumentsDone)
	case relay.MaheshvaraEventUsageDelta:
		if event.Usage != nil {
			a.usage = event.Usage
		}
	case relay.MaheshvaraEventResponseCompleted:
		if event.Usage != nil {
			a.usage = event.Usage
		} else if event.Response != nil && event.Response.Usage != nil {
			a.usage = event.Response.Usage
		}
		if event.FinishReason != "" {
			a.finish = event.FinishReason
		}
		return true
	case relay.MaheshvaraEventResponseFailed:
		message := "上游流式响应失败"
		if event.Error != nil && event.Error.Message != "" {
			message = event.Error.Message
		}
		a.failure = message
		return true
	}
	return false
}

func (a *agentStreamAccumulator) result() *agent.CallResult {
	return &agent.CallResult{
		Text:         a.text.String(),
		Reasoning:    a.reasoning.String(),
		ToolCalls:    a.toolCalls(),
		Usage:        a.usage,
		FinishReason: a.finish,
	}
}

// ---- 用户内容渲染（平台感知）----

// agentUserContentRenderer 按当前会话模型平台渲染用户消息：文本走文本块，
// 图片走 image 块，文档（PDF 等）按平台写成 data: URL（OpenAI 系）或裸
// base64（Claude/Gemini）。
type agentUserContentRenderer struct {
	server *Server
}

func newAgentUserContentRenderer(s *Server) *agentUserContentRenderer {
	return &agentUserContentRenderer{server: s}
}

func (r *agentUserContentRenderer) RenderUserContent(meta agent.SessionMeta, content *agent.UserContent) ([]relay.MaheshvaraContentPart, error) {
	format := relay.APIFormatChatCompletions
	if r.server != nil && r.server.store != nil {
		if model, ok := findCustomProtocolTestModel(context.Background(), r.server.store,
			meta.Settings.ModelSourceID, meta.Settings.ModelName); ok {
			format = relay.NormalizeAPIFormat(model.Platform)
		}
	}
	var parts []relay.MaheshvaraContentPart
	if text := strings.TrimSpace(content.Text); text != "" {
		parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText, Text: text})
	}
	if len(content.Documents) > agentDocMax {
		return nil, fmt.Errorf("附件最多 %d 个", agentDocMax)
	}
	total := len(content.Text)
	for index, doc := range content.Documents {
		text := strings.TrimSpace(doc.Text)
		dataURL := strings.TrimSpace(doc.DataURL)
		if text == "" && dataURL == "" {
			continue
		}
		switch {
		case text != "":
			if len(text) > agentDocMaxText {
				return nil, fmt.Errorf("材料 %d（%s）文本过长（上限 %d 字节）", index+1, doc.Name, agentDocMaxText)
			}
			total += len(text)
			label := doc.Name
			if label == "" {
				label = fmt.Sprintf("材料 %d", index+1)
			}
			parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText,
				Text: fmt.Sprintf("\n===== 材料 %d：%s =====\n%s\n===== 材料 %d 结束 =====", index+1, label, text, index+1)})
		case dataURL != "":
			mime, base64Data, err := parseAgentDataURL(dataURL)
			if err != nil {
				return nil, fmt.Errorf("材料 %d（%s）: %v", index+1, doc.Name, err)
			}
			if len(base64Data) > agentDocMaxFile {
				return nil, fmt.Errorf("材料 %d（%s）过大（上限 %d MiB）", index+1, doc.Name, agentDocMaxFile>>20)
			}
			total += len(base64Data)
			if mime == "" {
				mime = doc.Mime
			}
			label := doc.Name
			if label == "" {
				label = fmt.Sprintf("attachment-%d", index+1)
			}
			if strings.HasPrefix(mime, "image/") {
				parts = append(parts, relay.MaheshvaraContentPart{
					Type: relay.MaheshvaraContentImage, ImageBase64: string(base64Data),
					MediaType: mime, FileName: label,
				})
			} else {
				fileData := string(base64Data)
				switch format {
				case relay.APIFormatChatCompletions, relay.APIFormatResponses:
					fileData = dataURL
				}
				parts = append(parts, relay.MaheshvaraContentPart{
					Type: relay.MaheshvaraContentDocument, FileData: fileData,
					MediaType: mime, FileName: label,
				})
			}
		}
		if total > agentDocMaxTotal {
			return nil, fmt.Errorf("输入材料总量超过 %d MiB 上限", agentDocMaxTotal>>20)
		}
	}
	return parts, nil
}

func parseAgentDataURL(dataURL string) (mime string, data []byte, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", nil, fmt.Errorf("dataUrl 必须以 data: 开头")
	}
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return "", nil, fmt.Errorf("dataUrl 缺少逗号分隔符")
	}
	header := dataURL[5:comma]
	payload := dataURL[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return "", nil, fmt.Errorf("dataUrl 仅支持 base64 编码")
	}
	mime = strings.TrimSuffix(header, ";base64")
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("base64 解码失败: %w", err)
	}
	return mime, data, nil
}
