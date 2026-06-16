package server

import (
	"encoding/json"
	"fmt"
	"io"
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

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	record := s.initUsageRecord(c, startTime, bodyBytes, relay.FormatResponses)
	record.SourceFormat = string(relay.FormatResponses)
	record.SourceEndpoint = "/v1/responses"
	installDownstreamCapture(c, record)

	responsesCfg := s.config.GetResponsesConfig()
	if responsesCfg.Enabled != nil && !*responsesCfg.Enabled {
		s.failRequestTyped(c, record, startTime, http.StatusNotFound, "unsupported_endpoint", "Responses API is disabled")
		return
	}

	canonicalReq, originalResponsesReq, err := relay.ResponsesRequestToCanonical(bodyBytes)
	if err != nil {
		s.failRequestTyped(c, record, startTime, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	// 模型组级访问权限：先于 validateModelGroup 校验，越权即使目标组为空也返回 403。
	if !s.tokenAllowsGroup(c, canonicalReq.Model) {
		s.failRequestTyped(c, record, startTime, http.StatusForbidden, "permission_error", fmt.Sprintf("api key is not allowed to access model group '%s'", canonicalReq.Model))
		return
	}

	group, err := s.validateModelGroup(canonicalReq.Model)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			statusCode = http.StatusNotFound
		} else if strings.Contains(err.Error(), "disabled") {
			statusCode = http.StatusForbidden
		}
		s.failRequestTyped(c, record, startTime, statusCode, "invalid_request_error", err.Error())
		return
	}
	setRecordGroup(record, group)

	// 构建有序候选并按渠道亲和性置顶，与 chatCompletions 对齐——Responses 入口
	// 此前只取单个候选、无故障转移（C1）。空候选集显式返回 500「无可用模型」，
	// 而非让空 baseUrl 掉进 SSRF 校验误报 403。
	candidates := s.buildCandidates(group)
	if len(candidates) == 0 {
		s.failRequestTyped(c, record, startTime, http.StatusInternalServerError, "api_error", fmt.Sprintf("no available models in group '%s'", group.Name))
		return
	}
	if sticky := s.affinity.get(record.KeyHash, group.ID, startTime); sticky != "" {
		candidates = applyAffinity(candidates, sticky)
	}

	estimatedUsage := estimateCanonicalRequestUsage(canonicalReq, s.config.GetUsageConfig())
	estimatedTokens := estimatedUsage.EstimatedTotalTokens
	record.Usage = usageTokenUsageFromCanonical(estimatedUsage)
	record.UsageDetail = usageDetailFromCanonical(estimatedUsage)
	record.UsageSource = estimatedUsage.Source

	releaseLimiter, err := s.acquireRateLimit(group, estimatedTokens)
	if err != nil {
		s.failRequestTyped(c, record, startTime, http.StatusTooManyRequests, "rate_limit_error", err.Error())
		return
	}
	defer releaseLimiter()

	attempts := maxAttempts(group.MaxRetries, len(candidates))
	var lastStatus int
	var lastErr string
	committed := false

	for attempt := 0; attempt < attempts; attempt++ {
		selectedModel := candidates[attempt]
		isLast := attempt == attempts-1

		// SSRF 出站校验（连接时还会再校验一次实际 IP，见 secureControl）。
		if err := s.validateOutbound(selectedModel.BaseURL); err != nil {
			lastStatus = http.StatusForbidden
			lastErr = fmt.Sprintf("target baseUrl rejected: %v", err)
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				record.StatusCode = lastStatus
				record.Error = lastErr
				record.EndedAt = time.Now()
				record.DurationMs = time.Since(startTime).Milliseconds()
				s.recordUsage(record)
				c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": lastErr, "type": "invalid_request_error"}})
				committed = true
			}
			continue
		}

		targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
		setRecordModel(record, selectedModel, targetPlatform)
		canonicalReq.Model = selectedModel.Name

		targetFormat, responsesMode, err := selectResponsesTargetFormat(selectedModel, targetPlatform, responsesCfg)
		if err != nil {
			// 该候选不支持 Responses（或转换目标）——其他候选可能支持，故可重试。
			lastStatus = http.StatusBadRequest
			lastErr = err.Error()
			record.ResponsesMode = responsesMode
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				record.StatusCode = lastStatus
				record.Error = lastErr
				record.EndedAt = time.Now()
				record.DurationMs = time.Since(startTime).Milliseconds()
				s.recordUsage(record)
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": lastErr, "type": "unsupported_endpoint", "code": "responses_api_not_supported"}})
				committed = true
			}
			continue
		}

		record.TargetFormat = string(targetFormat)
		record.TargetEndpoint = targetEndpointForFormat(targetFormat)
		record.RelayMode = responsesMode
		record.ResponsesMode = responsesMode
		record.ConversionChain = []string{"openai_responses_request", "canonical_request", string(targetFormat) + "_request"}

		// 上游原生支持 Responses API（targetFormat=FormatResponses）时走「透传」：
		// 以客户端原始请求体为基底，只改写 model 名，其余字段原样保留。
		var targetBody []byte
		if targetFormat == relay.FormatResponses {
			targetBody, err = relay.ResponsesPassthroughBody(bodyBytes, selectedModel.Name)
		} else {
			targetBody, err = relay.CanonicalToTargetRequest(canonicalReq, targetFormat, originalResponsesReq)
		}
		if err != nil {
			lastStatus = http.StatusBadRequest
			lastErr = err.Error()
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				record.StatusCode = lastStatus
				record.Error = lastErr
				record.EndedAt = time.Now()
				record.DurationMs = time.Since(startTime).Milliseconds()
				s.recordUsage(record)
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": lastErr, "type": "invalid_request_error"}})
				committed = true
			}
			continue
		}
		record.OutgoingBody = sanitizeUsageBody(targetBody)

		var outcome relayOutcome
		if canonicalReq.Stream {
			record.Stream = true
			outcome = s.handleResponsesStream(c, group, selectedModel, targetBody, targetPlatform, targetFormat, startTime, estimatedTokens, record, isLast)
		} else {
			outcome = s.handleResponsesNormal(c, group, selectedModel, targetBody, targetPlatform, targetFormat, startTime, estimatedTokens, record, isLast)
		}

		if outcome.committed {
			committed = true
			if outcome.statusCode >= 200 && outcome.statusCode < 300 {
				s.affinity.set(record.KeyHash, group.ID, selectedModel.Name, startTime)
			}
			break
		}

		lastStatus = outcome.statusCode
		lastErr = outcome.errMsg
		s.appendRetryEvent(record, attempt, selectedModel.Name, outcome.errMsg)
		if !isLast && group.RetryInterval > 0 {
			time.Sleep(time.Duration(group.RetryInterval) * time.Millisecond)
		}
	}

	if !committed {
		if lastStatus == 0 {
			lastStatus = http.StatusBadGateway
		}
		record.StatusCode = lastStatus
		record.Error = lastErr
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": firstNonEmpty(lastErr, "all upstream attempts failed"), "type": "api_error"}})
	}
}

func (s *Server) handleResponsesNormal(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord, isLast bool) relayOutcome {
	// failResult 决定：最后一次尝试或不可重试状态码 → 向客户端提交错误响应；
	// 否则返回 committed=false 让上层故障转移到下一个候选。
	failResult := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		retryable := shouldRetryStatus(statusCode)
		if isLast || !retryable {
			record.StatusCode = statusCode
			record.Error = errMsg
			if respBody != nil {
				c.Data(statusCode, "application/json", respBody)
			} else {
				c.JSON(statusCode, gin.H{"error": gin.H{"message": errMsg, "type": "api_error"}})
			}
			return relayOutcome{committed: true, statusCode: statusCode, errMsg: errMsg}
		}
		return relayOutcome{committed: false, statusCode: statusCode, errMsg: errMsg}
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

	var canonicalResp *relay.CanonicalResponse

	switch targetFormat {
	case relay.FormatResponses:
		responsesResp, respBody, upstreamStatus, err := s.openaiAdapter.SendResponsesRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			status := upstreamStatus
			if status <= 0 {
				status = http.StatusBadGateway
			}
			result = failResult(status, err.Error(), respBody)
			return result
		}
		canonicalResp, err = relay.ResponsesResponseToCanonical(responsesResp)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}
		record.ConversionChain = append(record.ConversionChain, "openai_responses_response")
		updateRecordUsageFromCanonical(record, canonicalResp.Usage)
		applyLocalResponseEstimate(record, extractOutputTextFromCanonicalResponse(canonicalResp), s.config.GetUsageConfig())
		actualTokens := getInt(record.Usage.TotalTokens)
		s.adjustTokenUsage(group.ID, actualTokens)
		record.StatusCode = http.StatusOK
		c.Data(http.StatusOK, "application/json", respBody)
		result = relayOutcome{committed: true, statusCode: http.StatusOK}
		return result

	case relay.FormatClaude:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, false)
		if err != nil {
			result = failResult(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody)
			return result
		}
		var claudeResp relay.ClaudeResponse
		respBody, err := readBodyAndJSON(httpResp, &claudeResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}
		canonicalResp, err = relay.ClaudeResponseToCanonical(&claudeResp)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}

	case relay.FormatGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, false)
		if err != nil {
			result = failResult(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody)
			return result
		}
		var geminiResp relay.GeminiResponse
		respBody, err := readBodyAndJSON(httpResp, &geminiResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}
		canonicalResp, err = relay.GeminiResponseToCanonical(&geminiResp)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}

	default:
		openAIResp, respBody, statusCode, err := s.openaiAdapter.SendRequestRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			if statusCode <= 0 {
				statusCode = http.StatusBadGateway
			}
			result = failResult(statusCode, err.Error(), respBody)
			return result
		}
		canonicalResp, err = relay.OpenAIChatResponseToCanonical(openAIResp)
		if err != nil {
			result = failResult(http.StatusInternalServerError, err.Error(), nil)
			return result
		}
	}

	if canonicalResp.Model == "" {
		canonicalResp.Model = selectedModel.Name
	}
	record.ConversionChain = append(record.ConversionChain, string(targetFormat)+"_response", "canonical_response", "openai_responses_response")
	updateRecordUsageFromCanonical(record, canonicalResp.Usage)
	applyLocalResponseEstimate(record, extractOutputTextFromCanonicalResponse(canonicalResp), s.config.GetUsageConfig())
	actualTokens := getInt(record.Usage.TotalTokens)
	s.adjustTokenUsage(group.ID, actualTokens)

	responsesResp, err := relay.CanonicalToResponsesResponse(canonicalResp)
	if err != nil {
		result = failResult(http.StatusInternalServerError, err.Error(), nil)
		return result
	}

	record.StatusCode = http.StatusOK
	c.JSON(http.StatusOK, responsesResp)
	result = relayOutcome{committed: true, statusCode: http.StatusOK}
	return result
}

func (s *Server) handleResponsesStream(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord, isLast bool) relayOutcome {
	var result relayOutcome
	defer func() {
		if !result.committed {
			return
		}
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()

	// connFail 处理「SSE 尚未开始」的上游建连失败：可重试且非最后一次 →
	// committed=false 让上层换下一个候选；否则写出 JSON 错误并提交。
	connFail := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		retryable := shouldRetryStatus(statusCode)
		if isLast || !retryable {
			record.StatusCode = statusCode
			record.Error = errMsg
			if respBody != nil {
				c.Data(statusCode, "application/json", respBody)
			} else {
				c.AbortWithStatusJSON(statusCode, gin.H{"error": gin.H{"message": errMsg, "type": "api_error"}})
			}
			return relayOutcome{committed: true, statusCode: statusCode, errMsg: errMsg}
		}
		return relayOutcome{committed: false, statusCode: statusCode, errMsg: errMsg}
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Streaming not supported", "type": "api_error"}})
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
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		sseStarted = true
	}

	writer := &observingStreamWriter{
		inner:        &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:       record,
		startTime:    startTime,
		observeUsage: true,
	}

	var streamErr error
	switch targetFormat {
	case relay.FormatResponses:
		resp, err := s.openaiAdapter.SendResponsesStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			result = connFail(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		startSSE()
		observeUpstreamUsage(resp, record, targetPlatform, targetFormat)
		streamErr = relay.ForwardResponsesStream(c.Request.Context(), resp, writer)
	case relay.FormatClaude:
		resp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
		if err != nil {
			result = connFail(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		// 上游非 200：body 是 JSON 错误而非 SSE，转换器会扫不到 data: 行、
		// 发出伪造的空流并吞掉错误（R3）。SSE 尚未开始，可走 connFail 故障转移。
		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			result = connFail(resp.StatusCode, string(respBody), respBody)
			return result
		}
		startSSE()
		observeUpstreamUsage(resp, record, targetPlatform, targetFormat)
		streamErr = relay.ConvertClaudeStreamToResponsesStream(resp, writer, selectedModel.Name)
	case relay.FormatGemini:
		resp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, true)
		if err != nil {
			result = connFail(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			result = connFail(resp.StatusCode, string(respBody), respBody)
			return result
		}
		startSSE()
		observeUpstreamUsage(resp, record, targetPlatform, targetFormat)
		streamErr = relay.ConvertGeminiStreamToResponsesStream(resp, writer, selectedModel.Name)
	default:
		resp, err := s.openaiAdapter.SendRequestStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			result = connFail(http.StatusBadGateway, err.Error(), nil)
			return result
		}
		startSSE()
		observeUpstreamUsage(resp, record, targetPlatform, targetFormat)
		streamErr = relay.ConvertOpenAIChatStreamToResponsesStream(resp, writer, selectedModel.Name)
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
		writeResponsesStreamError(writer, streamErr)
	} else if streamYieldedNothing(record, writer) {
		// 上游返回 200 但既无输出文本也无 usage —— 实际空响应，纠正为失败。
		log.Printf("Upstream Responses stream returned empty response (no content, no usage)")
		record.Error = "upstream returned empty response"
		record.StatusCode = http.StatusBadGateway
		writeResponsesStreamError(writer, fmt.Errorf("upstream returned empty response"))
	}

	applyLocalResponseEstimate(record, writer.responseText.String(), s.config.GetUsageConfig())
	actualTokens := getInt(record.Usage.TotalTokens)
	s.adjustTokenUsage(group.ID, actualTokens)
	// SSE 已开始即无法再改 HTTP 状态码/换上游，本次必然提交（无论流中途是否出错）。
	result = relayOutcome{committed: true, statusCode: record.StatusCode}
	return result
}

// writeResponsesStreamError 向已开始的 SSE 流写一个 error 事件作为收尾，
// 用于上游中途断流等场景，避免下游看到"无收尾的突然断开"。
func writeResponsesStreamError(writer relay.StreamResponseWriter, err error) {
	payload, merr := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "upstream_stream_error",
			"message": err.Error(),
		},
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
		return relay.FormatResponses, "native_responses", nil
	}

	if mode == "native" {
		return "", "native_responses", fmt.Errorf("selected upstream model %q does not declare Responses API support", model.Name)
	}

	if mode != "auto" && mode != "transform" {
		return "", mode + "_responses", fmt.Errorf("unsupported Responses upstreamMode %q", responsesCfg.UpstreamMode)
	}

	targetFormat, ok := transformedResponsesTargetFormat(model, platform)
	if !ok {
		return "", mode + "_responses", fmt.Errorf("selected upstream model %q does not declare a transformable endpoint for Responses API", model.Name)
	}
	return targetFormat, "transformed_responses", nil
}

func transformedResponsesTargetFormat(model config.ModelRef, platform relay.Platform) (relay.FormatType, bool) {
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
