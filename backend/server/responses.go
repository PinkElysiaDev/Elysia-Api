package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func (s *Server) responses(c *gin.Context) {
	startTime := time.Now()

	bodyBytes, ok := s.readRequestBody(c)
	if !ok {
		return
	}

	record := s.initUsageRecord(c, startTime, bodyBytes, relay.FormatResponses)
	record.SourceFormat = string(relay.FormatResponses)
	record.SourceEndpoint = "/v1/responses"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))

	responsesCfg := s.config.GetResponsesConfig()
	if responsesCfg.Enabled != nil && !*responsesCfg.Enabled {
		s.failRequestError(c, record, startTime, relay.FormatResponses, &relay.MaheshvaraError{
			Class: relay.ErrorClassInvalidRequest, Status: http.StatusNotFound,
			Code: "unsupported_endpoint", Message: "Responses API is disabled",
		})
		return
	}

	maheshvaraReq, originalResponsesReq, err := relay.OpenAIResponsesToMaheshvara(bodyBytes)
	if err != nil {
		s.failRequestError(c, record, startTime, relay.FormatResponses, &relay.MaheshvaraError{
			Class: relay.ErrorClassInvalidRequest, Message: err.Error(),
		})
		return
	}

	// 共用前置阶段（与 chatCompletions 同一实现）：鉴权 → 组校验 → 候选 →
	// 能力约束 → 预估 → 限流。组级 MaxTokens 覆盖维持 chat 线制独有的行为。
	plan, ok := s.prepareRelayPlan(c, record, startTime, maheshvaraReq, relayFailer{s: s, c: c, record: record, startTime: startTime, format: relay.FormatResponses}, false)
	if !ok {
		return
	}
	group, candidates := plan.group, plan.candidates
	filteredVision := plan.filtered
	defer plan.releaseLimiter()

	s.runRelayAttempts(c, record, startTime, group, candidates, relay.FormatResponses,
		func(attempt int, selectedModel config.ModelRef, isLast bool) relayAttemptStep {
			targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
			setRecordModel(record, selectedModel, targetPlatform)
			maheshvaraReq.Model = selectedModel.Name

			targetFormat, responsesMode, err := selectResponsesTargetFormat(selectedModel, targetPlatform, responsesCfg)
			if err != nil {
				// 该候选不支持 Responses（或转换目标）——其他候选可能支持，故可重试。
				record.ResponsesMode = responsesMode
				return relayAttemptStep{
					skipErr:    err,
					skipStatus: http.StatusBadRequest,
					skipClass:  relay.ErrorClassInvalidRequest,
				}
			}
			if filteredVision && targetFormat == relay.FormatResponses {
				transformedFormat, ok := transformedResponsesTargetFormat(selectedModel, targetPlatform)
				if !ok || transformedFormat == relay.FormatResponses {
					skipErr := fmt.Errorf("Responses target cannot represent the filtered maheshvara vision input")
					return relayAttemptStep{
						skipErr:    skipErr,
						skipStatus: http.StatusBadRequest,
						skipClass:  relay.ErrorClassInvalidRequest,
					}
				}
				targetFormat = transformedFormat
				responsesMode = ResponsesModeTransformed
			}

			if relay.IsCustomPlatform(targetPlatform) {
				record.TargetFormat = string(targetPlatform)
				if protocol, exists := relay.GetCustomProtocol(relay.CustomProtocolID(targetPlatform)); exists {
					record.TargetEndpoint = protocol.Request.PathTemplate
				}
			} else {
				record.TargetFormat = string(targetFormat)
				record.TargetEndpoint = targetEndpointForFormat(targetFormat)
			}
			record.RelayMode = responsesMode
			record.ResponsesMode = responsesMode
			record.ConversionChain = []string{"openai_responses_request", "maheshvara_request", string(targetFormat) + "_request"}

			// 组装发往上游的请求体（自定义协议 / 同协议透传 / 按线制转换）。
			targetBody, customRequest, buildErr := s.buildResponsesTargetBody(bodyBytes, maheshvaraReq, originalResponsesReq, selectedModel, targetPlatform, targetFormat, filteredVision, record)
			if buildErr != nil {
				return relayAttemptStep{
					skipErr:    buildErr,
					skipStatus: http.StatusBadRequest,
					skipClass:  relay.ErrorClassInvalidRequest,
				}
			}
			record.OutgoingBody = record.sanitizeBody(targetBody)

			if maheshvaraReq.Stream {
				record.Stream = true
				return relayAttemptStep{outcome: s.handleResponsesStream(c, group, selectedModel, targetBody, customRequest, targetPlatform, targetFormat, startTime, record, isLast)}
			}
			return relayAttemptStep{outcome: s.handleResponsesNormal(c, group, selectedModel, targetBody, customRequest, targetPlatform, targetFormat, startTime, record, isLast)}
		})
}

func (s *Server) handleResponsesNormal(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
	if relay.IsCustomPlatform(targetPlatform) {
		return s.relayCustomResponsesNormal(c, group, selectedModel, customRequest, targetPlatform, startTime, record, isLast)
	}
	// failResult 决定：最后一次尝试或不可重试状态码 → 向客户端提交错误响应；
	// 否则返回 committed=false 让上层故障转移到下一个候选。
	failResult := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		return relayFailOutcome(record, isLast, shouldRetryStatus(statusCode), statusCode, errMsg, func() {
			if respBody != nil {
				writeUpstreamError(c, relay.FormatResponses, targetPlatform, statusCode, respBody, contentTypeJSON)
				return
			}
			writeProtocolError(c, relay.FormatResponses, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: statusCode, Message: errMsg})
		})
	}

	var result relayOutcome
	defer func() {
		if !result.committed {
			return
		}
		if record.FirstByteMs == 0 {
			record.FirstByteMs = time.Since(startTime).Milliseconds()
		}
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()

	// 统一取回:四类上游分支的「发送→判错→非 2xx 读体→转 Maheshvara」
	// 骨架收敛于 fetchAsMaheshvara(与 chat 入口同一实现)。
	fetched, err := s.fetchAsMaheshvara(c.Request.Context(), selectedModel, targetFormat, targetBody)
	if fetched.respBody != nil {
		record.ProviderResponse = record.sanitizeBody(fetched.respBody)
	}
	if err != nil {
		status := fetched.status
		if status <= 0 {
			status = http.StatusBadGateway
		}
		result = failResult(status, err.Error(), fetched.respBody)
		return result
	}
	maheshvaraResp := fetched.maheshvara
	record.ConversionChain = append(record.ConversionChain, string(targetFormat)+"_response")

	if maheshvaraResp.Model == "" {
		maheshvaraResp.Model = selectedModel.Name
	}
	record.ConversionChain = append(record.ConversionChain, "maheshvara_response", "openai_responses_response")
	s.settleMaheshvaraUsage(group, record, startTime, maheshvaraResp)

	responsesResp, err := relay.MaheshvaraToOpenAIResponsesResponse(maheshvaraResp)
	if err != nil {
		result = failResult(http.StatusInternalServerError, err.Error(), nil)
		return result
	}

	record.StatusCode = http.StatusOK
	c.JSON(http.StatusOK, responsesResp)
	result = relayOutcome{committed: true, statusCode: http.StatusOK}
	return result
}

// buildResponsesTargetBody 组装 Responses 入口发往上游的请求体，三分叉：
// 自定义协议渲染；上游原生 Responses 且未做视觉过滤时以原始请求体零转换
// 透传（保留 reasoning/function_call 等富字段）；其余按目标线制转换。
// relayMode 随分支写入 record（自定义协议保持调用方已设的 responsesMode）。
func (s *Server) buildResponsesTargetBody(bodyBytes []byte, maheshvaraReq *relay.MaheshvaraRequest, originalResponsesReq *relay.OpenAIResponsesRequest, selectedModel config.ModelRef, targetPlatform relay.Platform, targetFormat relay.FormatType, filteredVision bool, record *usageRecord) ([]byte, *relay.CustomProtocolRequestResult, error) {
	if relay.IsCustomPlatform(targetPlatform) {
		customRequest, err := relay.RenderRegisteredCustomProtocolRequest(maheshvaraReq, relay.CustomProtocolID(targetPlatform))
		if err != nil {
			return nil, nil, err
		}
		return customRequest.Body, customRequest, nil
	}
	if targetFormat == relay.FormatResponses && !filteredVision {
		targetBody, err := relay.ResponsesPassthroughBody(bodyBytes, selectedModel.Name)
		if err == nil {
			record.RelayMode = RelayModePassthrough
		}
		return targetBody, nil, err
	}
	targetBody, err := relay.MaheshvaraToTargetRequest(maheshvaraReq, targetFormat, originalResponsesReq)
	if err == nil {
		record.RelayMode = RelayModeTransform
	}
	return targetBody, nil, err
}

func (s *Server) handleResponsesStream(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
	if relay.IsCustomPlatform(targetPlatform) {
		return s.handleCustomStreamRequest(c, group, selectedModel, customRequest, targetPlatform, relay.FormatResponses, startTime, record, isLast)
	}
	var result relayOutcome
	defer func() {
		if !result.committed {
			return
		}
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()

	// upstreamErrorStatus 从错误中提取上游真实状态码：永久错误（401/403/400）
	// 不得洗白成 502 触发全候选扇出重试。
	// connFail 处理「SSE 尚未开始」的上游建连失败：可重试且非最后一次 →
	// committed=false 让上层换下一个候选；否则写出 JSON 错误并提交。
	connFail := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		return relayFailOutcome(record, isLast, shouldRetryStatus(statusCode), statusCode, errMsg, func() {
			if respBody != nil {
				writeUpstreamError(c, relay.FormatResponses, targetPlatform, statusCode, respBody, contentTypeJSON)
				return
			}
			writeProtocolError(c, relay.FormatResponses, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: statusCode, Message: errMsg})
		})
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		writeProtocolError(c, relay.FormatResponses, &relay.MaheshvaraError{Class: relay.ErrorClassServer, Message: "streaming is not supported on this connection"})
		result = relayOutcome{committed: true, statusCode: http.StatusInternalServerError}
		return result
	}

	// SSE 响应头延后到上游连接成功、即将写出响应体之前再设置（借鉴 new-api /
	// handleStreamRequest）。这样上游快速失败时还没设流式头，AbortWithStatusJSON
	// 能干净返回 JSON 错误（带 Content-Length），不会和 Transfer-Encoding 冲突。
	// 不手动设 Transfer-Encoding：Go 的 http.Server 对无 Content-Length 的流式
	// 响应自动 chunked，手动设反而在错误路径制造 TE + Content-Length 冲突，
	// 导致 codex 等客户端判定响应损坏、立即断连、不断重试。
	sseStarted := false
	startSSE := func() {
		if sseStarted {
			return
		}
		writeSSEHeaders(c.Writer)
		sseStarted = true
	}

	writer := &observingStreamWriter{
		inner:     &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:    record,
		startTime: startTime,
	}

	var streamErr error
	conn, failure := s.openUpstreamStream(c.Request.Context(), targetFormat, selectedModel, targetBody)
	if failure != nil {
		if failure.transport {
			err := failure.err
			result = connFail(upstreamErrorStatus(err, http.StatusBadGateway), err.Error(), upstreamErrorBody(err))
			return result
		}
		result = connFail(failure.status, string(failure.body), failure.body)
		return result
	}
	startSSE()
	observeUpstreamUsage(conn.resp, record, targetPlatform, targetFormat)
	if record.RelayMode == RelayModePassthrough && conn.format == relay.FormatResponses {
		// 同协议透传：原样转发上游 SSE，保留 reasoning_text 等
		// provider 私有事件，不再经 Maheshvara 解码重渲染。
		streamErr = relay.ForwardResponsesStream(c.Request.Context(), conn.resp, writer)
	} else {
		streamErr = relay.TransformStreamViaMaheshvara(c.Request.Context(), conn.resp, conn.format, relay.FormatResponses, writer, selectedModel.Name)
	}

	// 流式转发中途出错（如上游断流/空响应）：向下游写一个 SSE error 终止事件，让客户端能
	// 明确感知"出错了"，而非看到连接莫名中断、无任何收尾。
	if streamErr != nil {
		log.Printf("Error forwarding Responses stream: %v", streamErr)
		record.Error = streamErr.Error()
		// 转发中途出错必须反映为失败状态码，否则会被统计/日志误判为成功（200）。
		// 上游已成功建连但流中断属上游侧问题，记 502。
		if record.StatusCode < 400 {
			record.StatusCode = http.StatusBadGateway
		}
		// 转换路径的 renderer.Abort 已写出规范收尾帧(error + response.failed,
		// 事件携带 sequence_number),重复补写会打乱事件序;仅纯转发失败需要补帧。
		var rendered *relay.MaheshvaraError
		if !errors.As(streamErr, &rendered) {
			writeResponsesStreamError(writer, streamErr)
		}
	} else if streamYieldedNothing(record, writer) {
		// 上游返回 200 但既无输出文本也无 usage —— 实际空响应，纠正为失败。
		log.Printf("Upstream Responses stream returned empty response (no content, no usage)")
		record.Error = "upstream returned empty response"
		record.StatusCode = http.StatusBadGateway
		writeResponsesStreamError(writer, fmt.Errorf("upstream returned empty response"))
	}

	s.settleStreamUsage(group, record, startTime)
	// SSE 已开始即无法再改 HTTP 状态码/换上游，本次必然提交（无论流中途是否出错）。
	result = relayOutcome{committed: true, statusCode: record.StatusCode}
	return result
}

// writeResponsesStreamError 向已开始的 SSE 流写一个 error 事件作为收尾，
// 用于上游中途断流等场景，避免下游看到"无收尾的突然断开"。
func writeResponsesStreamError(writer relay.StreamResponseWriter, err error) {
	// 官方规范:Responses 流的错误事件是平铺对象(无 error 包裹)。
	payload, merr := json.Marshal(map[string]any{
		"type":    "error",
		"code":    nil,
		"message": err.Error(),
		"param":   nil,
	})
	if merr != nil {
		return
	}
	_, _ = writer.WriteString("event: error\n")
	_, _ = writer.WriteString("data: " + string(payload) + "\n\n")
	_ = writer.Flush()
}

func selectResponsesTargetFormat(model config.ModelRef, platform relay.Platform, responsesCfg config.ResponsesConfig) (relay.FormatType, string, error) {
	mode := strings.ToLower(strings.TrimSpace(responsesCfg.UpstreamMode))
	if mode == "" {
		mode = "auto"
	}

	if endpointSupportsResponses(model, platform) {
		return relay.FormatResponses, ResponsesModeNative, nil
	}

	if mode == "native" {
		return "", ResponsesModeNative, fmt.Errorf("selected upstream model %q does not declare Responses API support", model.Name)
	}

	if mode != "auto" && mode != RelayModeTransform {
		return "", mode + "_responses", fmt.Errorf("unsupported Responses upstreamMode %q", responsesCfg.UpstreamMode)
	}

	targetFormat, ok := transformedResponsesTargetFormat(model, platform)
	if !ok {
		return "", mode + "_responses", fmt.Errorf("selected upstream model %q does not declare a transformable endpoint for Responses API", model.Name)
	}
	return targetFormat, ResponsesModeTransformed, nil
}

func transformedResponsesTargetFormat(model config.ModelRef, platform relay.Platform) (relay.FormatType, bool) {
	if relay.IsCustomPlatform(platform) {
		_, ok := relay.GetCustomProtocol(relay.CustomProtocolID(platform))
		return relay.FormatOpenAIChat, ok
	}
	if endpointSupportsClaudeMessages(model, platform) {
		return relay.FormatClaude, true
	}
	if endpointSupportsGeminiGenerateContent(model, platform) {
		return relay.FormatGemini, true
	}
	if endpointSupportsChatCompletions(model, platform) {
		return relay.FormatOpenAIChat, true
	}
	return "", false
}

// 端点能力判定改为以「线路 API（apiFormat）」为准：模型源在 UI 上明确选了哪种
// wire API，就只声明对应那一种端点能力。这样选 Chat Completions 的源不会被误判为
// 支持 Responses（旧逻辑 platform==openai 时把两者混为一谈，导致该转换的没转换）。
// 显式 Endpoints 覆盖仍优先。apiFormat 取自 model.Platform，经 NormalizeAPIFormat
// 在线兼容旧值（openai/openai-compatible→chat_completions 等）。

func endpointSupportsChatCompletions(model config.ModelRef, platform relay.Platform) bool {
	if model.Endpoints != nil && model.Endpoints.ChatCompletions != nil {
		return *model.Endpoints.ChatCompletions
	}
	return relay.NormalizeAPIFormat(model.Platform) == relay.APIFormatChatCompletions
}

func endpointSupportsClaudeMessages(model config.ModelRef, platform relay.Platform) bool {
	if model.Endpoints != nil && model.Endpoints.ClaudeMessages != nil {
		return *model.Endpoints.ClaudeMessages
	}
	return relay.NormalizeAPIFormat(model.Platform) == relay.APIFormatAnthropic
}

func endpointSupportsGeminiGenerateContent(model config.ModelRef, platform relay.Platform) bool {
	if model.Endpoints != nil && model.Endpoints.GeminiGenerateContent != nil {
		return *model.Endpoints.GeminiGenerateContent
	}
	return relay.NormalizeAPIFormat(model.Platform) == relay.APIFormatGemini
}

func endpointSupportsResponses(model config.ModelRef, platform relay.Platform) bool {
	if model.Endpoints != nil && model.Endpoints.Responses != nil {
		return *model.Endpoints.Responses
	}
	return relay.NormalizeAPIFormat(model.Platform) == relay.APIFormatResponses
}

func targetEndpointForFormat(format relay.FormatType) string {
	switch format {
	case relay.FormatResponses:
		return "/v1/responses"
	case relay.FormatClaude:
		return "/v1/messages"
	case relay.FormatGemini:
		return "/v1beta/models/{model}:generateContent"
	default:
		return "/v1/chat/completions"
	}
}
