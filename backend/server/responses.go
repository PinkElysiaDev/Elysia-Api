package server

import (
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

	responsesCfg := s.config.GetResponsesConfig()
	if responsesCfg.Enabled != nil && !*responsesCfg.Enabled {
		record.StatusCode = http.StatusNotFound
		record.Error = "Responses API is disabled"
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Responses API is disabled", "type": "unsupported_endpoint"}})
		return
	}

	canonicalReq, originalResponsesReq, err := relay.ResponsesRequestToCanonical(bodyBytes)
	if err != nil {
		record.StatusCode = http.StatusBadRequest
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
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
		record.StatusCode = statusCode
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(statusCode, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	setRecordGroup(record, group)

	selectedModel := s.selectModel(group)
	if err := validateOutboundBaseURL(selectedModel.BaseURL); err != nil {
		record.StatusCode = http.StatusForbidden
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": fmt.Sprintf("target baseUrl rejected: %v", err), "type": "invalid_request_error"}})
		return
	}

	targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
	setRecordModel(record, selectedModel, targetPlatform)

	canonicalReq.Model = selectedModel.Name
	if group.MaxTokens > 0 {
		canonicalReq.MaxOutputTokens = group.MaxTokens
	}

	targetFormat, responsesMode, err := selectResponsesTargetFormat(selectedModel, targetPlatform, responsesCfg)
	if err != nil {
		record.StatusCode = http.StatusBadRequest
		record.Error = err.Error()
		record.ResponsesMode = responsesMode
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "unsupported_endpoint", "code": "responses_api_not_supported"}})
		return
	}

	record.TargetFormat = string(targetFormat)
	record.TargetEndpoint = targetEndpointForFormat(targetFormat)
	record.RelayMode = responsesMode
	record.ResponsesMode = responsesMode
	record.ConversionChain = []string{"openai_responses_request", "canonical_request", string(targetFormat) + "_request"}

	estimatedUsage := estimateCanonicalRequestUsage(canonicalReq, s.config.GetUsageConfig())
	estimatedTokens := estimatedUsage.EstimatedTotalTokens
	record.Usage = usageTokenUsageFromCanonical(estimatedUsage)
	record.UsageDetail = usageDetailFromCanonical(estimatedUsage)
	record.UsageSource = estimatedUsage.Source

	releaseLimiter, err := s.acquireRateLimit(group, estimatedTokens)
	if err != nil {
		record.StatusCode = http.StatusTooManyRequests
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": err.Error(), "type": "rate_limit_error"}})
		return
	}
	defer releaseLimiter(estimatedTokens)

	targetBody, err := relay.CanonicalToTargetRequest(canonicalReq, targetFormat, originalResponsesReq)
	if err != nil {
		record.StatusCode = http.StatusBadRequest
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	record.OutgoingBody = sanitizeUsageBody(targetBody)

	if canonicalReq.Stream {
		record.Stream = true
		s.handleResponsesStream(c, group, selectedModel, targetBody, targetPlatform, targetFormat, startTime, estimatedTokens, record)
		return
	}

	s.handleResponsesNormal(c, group, selectedModel, targetBody, targetPlatform, targetFormat, startTime, estimatedTokens, record)
}

func (s *Server) handleResponsesNormal(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord) {
	defer func() {
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
		responsesResp, respBody, err := s.openaiAdapter.SendResponsesRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.Data(http.StatusBadGateway, "application/json", respBody)
			return
		}
		canonicalResp, err = relay.ResponsesResponseToCanonical(responsesResp)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		record.ConversionChain = append(record.ConversionChain, "openai_responses_response")
		updateRecordUsageFromCanonical(record, canonicalResp.Usage)
		actualTokens := getInt(record.Usage.TotalTokens)
		s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)
		c.Data(http.StatusOK, "application/json", respBody)
		return

	case relay.FormatClaude:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, false)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			record.StatusCode = httpResp.StatusCode
			record.Error = string(respBody)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}
		var claudeResp relay.ClaudeResponse
		respBody, err := readBodyAndJSON(httpResp, &claudeResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		canonicalResp, err = relay.ClaudeResponseToCanonical(&claudeResp)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}

	case relay.FormatGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, false)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			record.StatusCode = httpResp.StatusCode
			record.Error = string(respBody)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}
		var geminiResp relay.GeminiResponse
		respBody, err := readBodyAndJSON(httpResp, &geminiResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		canonicalResp, err = relay.GeminiResponseToCanonical(&geminiResp)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}

	default:
		openAIResp, respBody, err := s.openaiAdapter.SendRequestRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.Data(http.StatusBadGateway, "application/json", respBody)
			return
		}
		canonicalResp, err = relay.OpenAIChatResponseToCanonical(openAIResp)
		if err != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Error = err.Error()
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
	}

	if canonicalResp.Model == "" {
		canonicalResp.Model = selectedModel.Name
	}
	record.ConversionChain = append(record.ConversionChain, string(targetFormat)+"_response", "canonical_response", "openai_responses_response")
	updateRecordUsageFromCanonical(record, canonicalResp.Usage)
	actualTokens := getInt(record.Usage.TotalTokens)
	s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)

	responsesResp, err := relay.CanonicalToResponsesResponse(canonicalResp)
	if err != nil {
		record.StatusCode = http.StatusInternalServerError
		record.Error = err.Error()
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
		return
	}

	c.JSON(http.StatusOK, responsesResp)
}

func (s *Server) handleResponsesStream(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, targetFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord) {
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Streaming not supported", "type": "api_error"}})
		return
	}
	writer := &observingStreamWriter{
		inner:        &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:       record,
		startTime:    startTime,
		observeUsage: true,
	}

	switch targetFormat {
	case relay.FormatResponses:
		resp, err := s.openaiAdapter.SendResponsesStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		observeUpstreamUsage(resp, record, targetPlatform)
		if err := relay.ForwardResponsesStream(resp, writer); err != nil {
			log.Printf("Error forwarding Responses stream: %v", err)
		}
	case relay.FormatClaude:
		resp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		observeUpstreamUsage(resp, record, targetPlatform)
		if err := relay.ConvertClaudeStreamToResponsesStream(resp, writer, selectedModel.Name); err != nil {
			log.Printf("Error converting Claude stream to Responses stream: %v", err)
		}
	case relay.FormatGemini:
		resp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, true)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		observeUpstreamUsage(resp, record, targetPlatform)
		if err := relay.ConvertGeminiStreamToResponsesStream(resp, writer, selectedModel.Name); err != nil {
			log.Printf("Error converting Gemini stream to Responses stream: %v", err)
		}
	default:
		resp, err := s.openaiAdapter.SendRequestStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			record.StatusCode = http.StatusBadGateway
			record.Error = err.Error()
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			return
		}
		observeUpstreamUsage(resp, record, targetPlatform)
		if err := relay.ConvertOpenAIChatStreamToResponsesStream(resp, writer, selectedModel.Name); err != nil {
			log.Printf("Error converting OpenAI chat stream to Responses stream: %v", err)
		}
	}

	if record.Usage.TotalTokens == nil && record.Usage.EstimatedTokens > 0 {
		record.Usage.TotalTokens = intPtr(record.Usage.EstimatedTokens)
	}
	actualTokens := getInt(record.Usage.TotalTokens)
	s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)
}

func selectResponsesTargetFormat(model config.ModelRef, platform relay.Platform, responsesCfg config.ResponsesConfig) (relay.FormatType, string, error) {
	mode := strings.ToLower(strings.TrimSpace(responsesCfg.UpstreamMode))
	if mode == "" {
		mode = "native"
	}

	nativeResponses := endpointSupportsResponses(model, platform)
	if nativeResponses {
		return relay.FormatResponses, "native_responses", nil
	}

	if mode == "native" {
		return "", "native_responses", fmt.Errorf("selected upstream model %q does not declare Responses API support", model.Name)
	}

	if mode == "auto" {
		behavior := strings.ToLower(strings.TrimSpace(responsesCfg.TransformUnsupportedBehavior))
		if behavior == "" || behavior == "error" {
			return "", "auto_responses", fmt.Errorf("selected upstream model %q does not declare Responses API support", model.Name)
		}
	}

	switch platform {
	case relay.PlatformAnthropic:
		return relay.FormatClaude, "transformed_responses", nil
	case relay.PlatformGemini:
		return relay.FormatGemini, "transformed_responses", nil
	default:
		return relay.FormatOpenAIChat, "transformed_responses", nil
	}
}

func endpointSupportsResponses(model config.ModelRef, platform relay.Platform) bool {
	if model.Endpoints != nil && model.Endpoints.Responses != nil {
		return *model.Endpoints.Responses
	}
	return platform == relay.PlatformOpenAI
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
