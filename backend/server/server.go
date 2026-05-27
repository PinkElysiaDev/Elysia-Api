package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

type rateLimitState struct {
	Date     string
	Requests int
	Tokens   int
	Active   int
}

type Server struct {
	config        *config.Config
	engine        *gin.Engine
	openaiAdapter *relay.OpenAIAdapter
	claudeAdapter *relay.ClaudeAdapter
	geminiAdapter *relay.GeminiAdapter
	// 轮询状态跟踪：模型组ID -> 当前模型索引
	roundRobinIndex map[string]int
	roundRobinMutex sync.Mutex

	rateLimitMu sync.Mutex
	rateLimits  map[string]*rateLimitState

	usageMu      sync.Mutex
	usageRecords []usageRecord
}

func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery(), gin.Logger())

	// 获取 HTTP 超时配置，默认 120 秒
	httpTimeout := time.Duration(cfg.HTTPTimeout) * time.Second
	if cfg.HTTPTimeout == 0 {
		httpTimeout = 0 // 0 表示不限制
	}

	server := &Server{
		config:          cfg,
		engine:          engine,
		openaiAdapter:   relay.NewOpenAIAdapter(httpTimeout),
		claudeAdapter:   relay.NewClaudeAdapter(httpTimeout),
		geminiAdapter:   relay.NewGeminiAdapter(httpTimeout),
		roundRobinIndex: make(map[string]int),
		rateLimits:      make(map[string]*rateLimitState),
	}
	if err := server.loadUsageRecords(); err != nil {
		log.Printf("failed to load usage records: %v", err)
	}
	return server
}

// logDebug 仅在调试模式下输出基本信息（模型组、选中模型、耗时）
func (s *Server) logDebug(format string, args ...interface{}) {
	if s.config.DebugMode {
		log.Printf(format, args...)
	}
}

// logVerbose 仅在详细日志模式下输出完整请求/响应结构
func (s *Server) logVerbose(format string, args ...interface{}) {
	if s.config.DebugMode && s.config.VerboseLog {
		log.Printf(format, args...)
	}
}

func compactLogJSON(data []byte) string {
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return string(data)
	}

	compacted, err := json.Marshal(obj)
	if err != nil {
		return string(data)
	}

	return string(compacted)
}

func isVisionCapable(group *config.ModelGroupConfig) bool {
	return group != nil && group.VisionCapable != nil && *group.VisionCapable
}

func filterVisionInputsIfNeeded(group *config.ModelGroupConfig, req *relay.UnifiedRequest) (changed bool, filteredMessages int, filteredParts int) {
	if req == nil || group == nil || isVisionCapable(group) {
		return false, 0, 0
	}

	newMessages, filteredMessages, filteredParts := filterUnifiedMessagesVisionContent(req.Messages)
	if filteredParts == 0 {
		return false, 0, 0
	}

	req.Messages = newMessages
	return true, filteredMessages, filteredParts
}

func filterUnifiedMessagesVisionContent(messages []relay.UnifiedMessage) ([]relay.UnifiedMessage, int, int) {
	if len(messages) == 0 {
		return messages, 0, 0
	}

	newMessages := make([]relay.UnifiedMessage, 0, len(messages))
	filteredMessages := 0
	filteredParts := 0

	for _, msg := range messages {
		newContent, removedParts, changed := filterSingleMessageVisionContent(msg.Content)
		if changed {
			filteredMessages++
			filteredParts += removedParts
			msg.Content = newContent
		}
		newMessages = append(newMessages, msg)
	}

	return newMessages, filteredMessages, filteredParts
}

func filterSingleMessageVisionContent(content interface{}) (newContent interface{}, removedParts int, changed bool) {
	if content == nil {
		return content, 0, false
	}

	parts, ok := content.([]interface{})
	if !ok {
		return content, 0, false
	}

	filtered := make([]interface{}, 0, len(parts))
	for _, part := range parts {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			filtered = append(filtered, part)
			continue
		}

		if isVisionPart(partMap) {
			removedParts++
			continue
		}

		filtered = append(filtered, part)
	}

	if removedParts == 0 {
		return content, 0, false
	}

	return normalizeFilteredContent(filtered), removedParts, true
}

func normalizeFilteredContent(parts []interface{}) interface{} {
	if len(parts) == 0 {
		return ""
	}

	if text, ok := mergeTextParts(parts); ok {
		return text
	}

	return parts
}

func mergeTextParts(parts []interface{}) (string, bool) {
	var builder strings.Builder

	for _, part := range parts {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			return "", false
		}

		if !isTextPart(partMap) {
			return "", false
		}

		text, _ := partMap["text"].(string)
		builder.WriteString(text)
	}

	return builder.String(), true
}

func isTextPart(item map[string]interface{}) bool {
	partType, _ := item["type"].(string)
	_, hasText := item["text"].(string)

	if hasText && (partType == "" || strings.EqualFold(partType, "text")) {
		return true
	}

	return false
}

func isVisionPart(item map[string]interface{}) bool {
	partType, _ := item["type"].(string)
	switch strings.ToLower(strings.TrimSpace(partType)) {
	case "image", "image_url", "input_image":
		return true
	}

	if _, ok := item["image_url"]; ok {
		return true
	}
	if _, ok := item["image"]; ok {
		return true
	}

	if inlineData, ok := item["inlineData"].(map[string]interface{}); ok {
		if isImageMime(extractMimeType(inlineData)) || inlineData["data"] != nil {
			return true
		}
	}

	if fileData, ok := item["fileData"].(map[string]interface{}); ok {
		if isImageMime(extractMimeType(fileData)) {
			return true
		}
	}

	if source, ok := item["source"].(map[string]interface{}); ok {
		if isImageMime(extractMimeType(source)) {
			return true
		}
	}

	if isImageMime(extractMimeType(item)) {
		return true
	}

	return false
}

func extractMimeType(item map[string]interface{}) string {
	for _, key := range []string{"mimeType", "mime_type", "media_type"} {
		if value, ok := item[key].(string); ok {
			return strings.TrimSpace(strings.ToLower(value))
		}
	}
	return ""
}

func isImageMime(mimeType string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(mimeType)), "image/")
}

func (s *Server) setupRoutes() {
	v1 := s.engine.Group("/v1")
	v1.Use(s.authMiddleware())
	{
		v1.POST("/chat/completions", s.chatCompletions)
		v1.POST("/responses", s.responses)               // OpenAI Responses API 入口
		v1.POST("/messages", s.chatCompletions)          // Claude 原生格式入口
		v1.POST("/messages/count_tokens", s.countTokens) // Claude 兼容 token 统计端点
		v1.GET("/models", s.listModels)
	}

	// Gemini 原生 API 兼容路由
	// /v1beta/models/MODEL:generateContent 和 /v1beta/models/MODEL:streamGenerateContent
	// gin 不支持参数内含冒号，用通配符捕获整段路径
	v1beta := s.engine.Group("/v1beta")
	v1beta.Use(s.authMiddleware())
	{
		v1beta.GET("/models", s.listGeminiModels)
		v1beta.POST("/models/*action", s.chatCompletions)
	}

	s.engine.GET("/usage", s.usageDashboard)

	usage := s.engine.Group("/__usage")
	usage.Use(s.dashboardAuthMiddleware())
	{
		usage.GET("/stats", s.usageStats)
		usage.GET("/logs", s.usageLogs)
		usage.GET("/logs/:id", s.usageLogDetail)
		usage.POST("/reset", s.resetUsage)
	}

	s.engine.GET("/health", s.healthCheck)
	s.engine.POST("/__reload", s.loopbackOnly(s.reloadConfig))
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		accessToken, ok := s.config.FindAccessToken(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}
		c.Set("elysiaKeyName", accessToken.Name)
		c.Set("elysiaKeyHash", shortTokenHash(token))
		c.Next()
	}
}

func (s *Server) dashboardAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		if !s.config.IsValidDashboardToken(token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "dashboard token is not configured or invalid",
			})
			return
		}
		c.Next()
	}
}

func extractAccessToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}

	apiKey := strings.TrimSpace(r.Header.Get("x-api-key"))
	if apiKey != "" {
		return apiKey
	}

	geminiHeaderKey := strings.TrimSpace(r.Header.Get("x-goog-api-key"))
	if geminiHeaderKey != "" {
		return geminiHeaderKey
	}

	queryKey := strings.TrimSpace(r.URL.Query().Get("key"))
	if queryKey != "" {
		return queryKey
	}

	return ""
}

func (s *Server) heartbeatLoopbackOnly(handler http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isLoopbackRequest(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "heartbeat endpoint is only available from loopback",
			})
			return
		}
		handler(c.Writer, c.Request)
	}
}

func (s *Server) loopbackOnly(handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isLoopbackRequest(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "reload endpoint is only available from loopback",
			})
			return
		}
		handler(c)
	}
}

func (s *Server) reloadConfig(c *gin.Context) {
	oldHost := s.config.Server.Host
	oldPort := s.config.Server.Port

	if err := s.config.Reload(); err != nil {
		log.Printf("Config reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"reloaded": false,
			"error":    err.Error(),
		})
		return
	}

	serverChanged := oldHost != s.config.Server.Host || oldPort != s.config.Server.Port
	if serverChanged {
		log.Printf(
			"Config hot-reloaded successfully, but server listen address change requires restart (old=%s:%d new=%s:%d)",
			oldHost,
			oldPort,
			s.config.Server.Host,
			s.config.Server.Port,
		)
	} else {
		log.Printf("Config hot-reloaded successfully")
	}

	c.JSON(http.StatusOK, gin.H{
		"reloaded":                     true,
		"debugMode":                    s.config.DebugMode,
		"verboseLog":                   s.config.VerboseLog,
		"serverChangedRequiresRestart": serverChanged,
		"server": gin.H{
			"oldHost": oldHost,
			"oldPort": oldPort,
			"newHost": s.config.Server.Host,
			"newPort": s.config.Server.Port,
		},
	})
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) chatCompletions(c *gin.Context) {
	s.logVerbose("[REQUEST ENTER] path=%s method=%s remote=%s contentType=%s", c.Request.URL.Path, c.Request.Method, c.Request.RemoteAddr, c.Request.Header.Get("Content-Type"))
	// 请求处理矩阵：inputFormat × targetPlatform
	//
	// 非流式 (handleNormalRequest):
	//   下游平台      | inputFormat=Claude              | inputFormat=OpenAI/其他
	//   Anthropic    | 直接返回 ClaudeResponse          | ConvertClaudeResponseToOpenAI
	//   Gemini       | ConvertGemini→OAI→Claude        | ConvertGeminiResponseToOpenAI
	//   OpenAI/其他  | ConvertOpenAIResponseToClaude   | 直接返回 OpenAIResponse
	//
	// 流式 (handleStreamRequest):
	//   下游平台      | inputFormat=Claude              | inputFormat=OpenAI/其他
	//   Anthropic    | ForwardStreamRaw（直接转发）      | ConvertClaudeStreamToOpenAI
	//   Gemini       | ConvertGeminiStreamToOpenAI     | ConvertGeminiStreamToOpenAI
	//   OpenAI/其他  | ConvertOpenAIStreamToClaudeStream | ForwardOpenAIStream（直接转发）
	startTime := time.Now()

	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		c.JSON(400, gin.H{"error": "Failed to read request body"})
		return
	}

	s.logVerbose("[Incoming Request Raw] %s", compactLogJSON(bodyBytes))

	// 根据请求路径判断客户端期望的输入/输出格式
	var inputFormat relay.FormatType
	switch {
	case strings.HasSuffix(c.Request.URL.Path, "/messages"):
		inputFormat = relay.FormatClaude
	case strings.HasPrefix(c.Request.URL.Path, "/v1beta/"):
		inputFormat = relay.FormatGemini
	default:
		inputFormat = relay.FormatOpenAI
	}
	record := s.initUsageRecord(c, startTime, bodyBytes, inputFormat)
	s.logVerbose("[Input Format] %s", inputFormat)

	// 转换为统一格式
	unifiedReq, err := relay.ConvertToUnified(bodyBytes, inputFormat)
	if err != nil {
		log.Printf("Error converting request: %v", err)
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	// Gemini 原生路径 /v1beta/models/MODEL:generateContent 中模型名在 URL 里
	// 若请求体没有 model 字段，从路径参数提取
	if unifiedReq.Model == "" {
		if action := c.Param("action"); action != "" {
			// action 形如 /gemini-2.0-flash:generateContent
			modelPart := strings.TrimPrefix(action, "/")
			if idx := strings.LastIndex(modelPart, ":"); idx != -1 {
				modelPart = modelPart[:idx]
			}
			unifiedReq.Model = modelPart
		}
	}

	if unifiedReqJSON, err := relay.MarshalUnifiedRequest(unifiedReq); err == nil {
		s.logVerbose("[Unified Request] %s", compactLogJSON(unifiedReqJSON))
	}

	// 验证并获取模型组
	group, err := s.validateModelGroup(unifiedReq.Model)
	if err != nil {
		statusCode := 500
		if errMsg := err.Error(); strings.Contains(errMsg, "not found") {
			statusCode = 404
		} else if strings.Contains(errMsg, "disabled") {
			statusCode = 403
		}
		record.StatusCode = statusCode
		record.Error = err.Error()
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	setRecordGroup(record, group)

	filtered, filteredMessages, filteredParts := filterVisionInputsIfNeeded(group, unifiedReq)
	if filtered {
		log.Printf(
			"[vision filter] model group %q is not vision-capable, filtered %d image part(s) from %d message(s)",
			group.Name,
			filteredParts,
			filteredMessages,
		)
		s.logVerbose(
			"[vision filter detail] group=%s filteredMessages=%d filteredImageParts=%d",
			group.Name,
			filteredMessages,
			filteredParts,
		)
		if unifiedReqJSON, err := relay.MarshalUnifiedRequest(unifiedReq); err == nil {
			s.logVerbose("[Unified Request After Vision Filter] %s", compactLogJSON(unifiedReqJSON))
		}
	}

	// 根据策略选择具体模型
	selectedModel := s.selectModel(group)
	s.logDebug("Request model group: '%s', selected: %s", group.Name, selectedModel.Name)

	if err := validateOutboundBaseURL(selectedModel.BaseURL); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("target baseUrl rejected: %v", err)})
		return
	}

	// 更新模型名称
	unifiedReq.Model = selectedModel.Name

	// 如果模型组配置了 MaxTokens，覆盖客户端发来的值
	if group.MaxTokens > 0 {
		unifiedReq.MaxTokens = group.MaxTokens
	}
	s.logDebug(
		"MaxTokens diagnostics: group=%s groupMaxTokens=%d effectiveRequestMaxTokens=%d",
		group.Name,
		group.MaxTokens,
		unifiedReq.MaxTokens,
	)

	estimatedTokens := estimateUnifiedRequestTokens(unifiedReq)
	releaseLimiter, err := s.acquireRateLimit(group, estimatedTokens)
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}
	defer releaseLimiter(estimatedTokens)

	// 检测目标平台
	targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
	setRecordModel(record, selectedModel, targetPlatform)
	s.logVerbose("[Target Platform] %s", targetPlatform)

	// 从统一格式转换为目标平台格式
	targetBody, err := relay.ConvertFromUnified(unifiedReq, targetPlatform)
	if err != nil {
		log.Printf("Error converting to target format: %v", err)
		record.StatusCode = http.StatusInternalServerError
		record.Error = fmt.Sprintf("Failed to convert request: %v", err)
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}
	record.OutgoingBody = sanitizeUsageBody(targetBody)

	s.logVerbose("[Outgoing Request] baseUrl=%s body=%s", selectedModel.BaseURL, compactLogJSON(targetBody))

	// 检查是否为流式请求
	// 规则：
	// 1) 请求体显式 stream=true
	// 2) Gemini 原生路径为 :streamGenerateContent（即使 body 不带 stream 字段）
	isStream := relay.IsStreamRequest(targetBody)
	if action := c.Param("action"); strings.Contains(action, ":streamGenerateContent") {
		isStream = true
	}

	// 当路径驱动判定为流式时，确保上游请求体也包含 stream=true
	// 避免出现“进入流式分支，但上游仍按非流式返回 JSON”的错配。
	if isStream {
		var streamBodyErr error
		targetBody, streamBodyErr = ensureStreamFlagInTargetBody(
			targetBody,
			targetPlatform,
		)
		if streamBodyErr != nil {
			log.Printf("Error ensuring stream flag in target body: %v", streamBodyErr)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to prepare stream request: %v", streamBodyErr)
			record.EndedAt = time.Now()
			record.DurationMs = time.Since(startTime).Milliseconds()
			s.recordUsage(record)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to prepare stream request: %v", streamBodyErr)})
			return
		}
		record.OutgoingBody = sanitizeUsageBody(targetBody)
		s.logVerbose("[Outgoing Stream Request Patched] body=%s", compactLogJSON(targetBody))
	}

	if isStream {
		record.Stream = true
		// 流式请求处理
		s.handleStreamRequest(c, group, selectedModel, targetBody, targetPlatform, inputFormat, startTime, estimatedTokens, record)
	} else {
		// 非流式请求处理
		s.handleNormalRequest(c, group, selectedModel, targetBody, targetPlatform, inputFormat, startTime, estimatedTokens, record)
	}
}

func (s *Server) handleNormalRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord) {
	defer func() {
		if record.FirstByteMs == 0 {
			record.FirstByteMs = time.Since(startTime).Milliseconds()
		}
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()
	// 设计原则：
	// 1) 先按 targetPlatform 获取并解析上游响应
	// 2) 再按 inputFormat 渲染客户端响应
	// 这样输入协议与下游平台彻底解耦，避免协议错配。
	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, false)
		if err != nil {
			log.Printf("Error forwarding Claude request: %v", err)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
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
			log.Printf("Error parsing Claude response: %v", err)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to parse response: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to parse response: %v", err)})
			return
		}

		record.Usage = usageFromProviderBody(targetPlatform, respBody)
		actualTokens := 0
		if record.Usage.TotalTokens != nil {
			actualTokens = *record.Usage.TotalTokens
		}
		s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)

		// 统一转为 OpenAI 中间响应，再渲染到客户端格式
		oaiResp := relay.ConvertClaudeResponseToOpenAI(&claudeResp)
		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		switch inputFormat {
		case relay.FormatClaude:
			c.JSON(200, claudeResp)
		case relay.FormatGemini:
			c.JSON(200, relay.ConvertOpenAIResponseToGemini(oaiResp))
		default:
			c.JSON(200, oaiResp)
		}

	case relay.PlatformGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, false)
		if err != nil {
			log.Printf("Error forwarding Gemini request: %v", err)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
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
			log.Printf("Error parsing Gemini response: %v", err)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to parse response: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to parse response: %v", err)})
			return
		}

		oaiResp := relay.ConvertGeminiResponseToOpenAI(&geminiResp)
		record.Usage = usageFromProviderBody(targetPlatform, respBody)
		actualTokens := 0
		if record.Usage.TotalTokens != nil {
			actualTokens = *record.Usage.TotalTokens
		}
		s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)

		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		switch inputFormat {
		case relay.FormatClaude:
			c.JSON(200, relay.ConvertOpenAIResponseToClaude(oaiResp))
		case relay.FormatGemini:
			c.JSON(200, geminiResp)
		default:
			c.JSON(200, oaiResp)
		}

	default:
		resp, respBody, err := s.openaiAdapter.SendRequestRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding request: %v", err)
			record.StatusCode = http.StatusInternalServerError
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
			return
		}

		record.ProviderResponse = sanitizeUsageBody(respBody)
		record.Usage = usageFromProviderBody(targetPlatform, respBody)
		actualTokens := 0
		if record.Usage.TotalTokens != nil {
			actualTokens = *record.Usage.TotalTokens
		}
		s.adjustTokenUsage(group.ID, estimatedTokens, actualTokens)

		s.logVerbose("=== Response ===")
		if respJSON, err := relay.MarshalResponse(resp); err == nil {
			s.logVerbose("%s", string(respJSON))
		}
		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		switch inputFormat {
		case relay.FormatClaude:
			c.JSON(200, relay.ConvertOpenAIResponseToClaude(resp))
		case relay.FormatGemini:
			c.JSON(200, relay.ConvertOpenAIResponseToGemini(resp))
		default:
			c.JSON(200, resp)
		}
	}
}

func (s *Server) handleStreamRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord) {
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()
	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported")
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		c.JSON(500, gin.H{"error": "Streaming not supported"})
		return
	}
	writer := &observingStreamWriter{
		inner:        &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:       record,
		startTime:    startTime,
		observeUsage: false,
	}

	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
		if err != nil {
			log.Printf("Error forwarding Claude stream request: %v", err)
			record.StatusCode = http.StatusBadGateway
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			writeStreamForwardError(c, inputFormat, err)
			return
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			record.StatusCode = httpResp.StatusCode
			record.Error = string(respBody)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

		observeUpstreamUsage(httpResp, record, targetPlatform)

		switch inputFormat {
		case relay.FormatClaude:
			if err := relay.ForwardStreamRaw(httpResp, writer); err != nil {
				log.Printf("Error streaming Claude response: %v", err)
			}
		case relay.FormatGemini:
			if err := relay.ConvertClaudeStreamToGeminiStream(httpResp, writer); err != nil {
				log.Printf("Error converting Claude stream to Gemini format: %v", err)
			}
		default:
			if err := relay.ConvertClaudeStreamToOpenAI(httpResp, writer); err != nil {
				log.Printf("Error converting Claude stream to OpenAI format: %v", err)
			}
		}

	case relay.PlatformGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, true)
		if err != nil {
			log.Printf("Error forwarding Gemini stream request: %v", err)
			record.StatusCode = http.StatusBadGateway
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			writeStreamForwardError(c, inputFormat, err)
			return
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			record.StatusCode = httpResp.StatusCode
			record.Error = string(respBody)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

		observeUpstreamUsage(httpResp, record, targetPlatform)

		switch inputFormat {
		case relay.FormatClaude:
			if err := relay.ConvertGeminiStreamToClaudeStream(httpResp, writer, selectedModel.Name); err != nil {
				log.Printf("Error converting Gemini stream to Claude format: %v", err)
			}
		case relay.FormatGemini:
			if err := relay.ForwardStreamRaw(httpResp, writer); err != nil {
				log.Printf("Error forwarding Gemini stream: %v", err)
			}
		default:
			if err := relay.ConvertGeminiStreamToOpenAI(httpResp, writer); err != nil {
				log.Printf("Error converting Gemini stream to OpenAI format: %v", err)
			}
		}

	default:
		resp, err := s.openaiAdapter.SendRequestStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding stream request: %v", err)
			record.StatusCode = http.StatusBadGateway
			record.Error = fmt.Sprintf("Failed to forward request: %v", err)
			writeStreamForwardError(c, inputFormat, err)
			return
		}

		observeUpstreamUsage(resp, record, targetPlatform)

		switch inputFormat {
		case relay.FormatClaude:
			if err := relay.ConvertOpenAIStreamToClaudeStream(resp, writer, selectedModel.Name); err != nil {
				log.Printf("Error converting OpenAI stream to Claude format: %v", err)
			}
		case relay.FormatGemini:
			if err := relay.ConvertOpenAIStreamToGeminiStream(resp, writer); err != nil {
				log.Printf("Error converting OpenAI stream to Gemini format: %v", err)
			}
		default:
			if err := relay.ForwardOpenAIStream(resp, writer); err != nil {
				log.Printf("Error forwarding OpenAI stream: %v", err)
			}
		}
	}

	s.logDebug("Stream request completed in %dms", time.Since(startTime).Milliseconds())
}

func readBodyAndJSON(resp *http.Response, v interface{}) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, json.Unmarshal(body, v)
}

// readJSON 从 HTTP 响应中读取并解析 JSON
func readJSON(resp *http.Response, v interface{}) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func writeStreamForwardError(
	c *gin.Context,
	inputFormat relay.FormatType,
	err error,
) {
	message := fmt.Sprintf("Failed to forward request: %v", err)

	switch inputFormat {
	case relay.FormatClaude:
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": message,
			},
		})
	default:
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
			"error": message,
		})
	}
}

// ensureStreamFlagInTargetBody 在需要流式转发时，为上游请求补齐 stream=true。
// 注意：Gemini 原生接口通过 URL action 决定是否流式，不应注入 stream 字段。
func ensureStreamFlagInTargetBody(
	targetBody []byte,
	targetPlatform relay.Platform,
) ([]byte, error) {
	if targetPlatform == relay.PlatformGemini {
		return targetBody, nil
	}

	var req map[string]interface{}
	if err := json.Unmarshal(targetBody, &req); err != nil {
		return nil, err
	}

	req["stream"] = true

	// OpenAI 兼容接口可附带 stream_options，帮助下游返回 usage chunk
	if targetPlatform == relay.PlatformOpenAI || targetPlatform == relay.PlatformDeepSeek || targetPlatform == relay.PlatformAzure {
		streamOptions, ok := req["stream_options"].(map[string]interface{})
		if !ok {
			streamOptions = map[string]interface{}{}
		}
		streamOptions["include_usage"] = true
		req["stream_options"] = streamOptions
	}

	return json.Marshal(req)
}

// ginStreamWriter 实现 relay.StreamResponseWriter，封装 gin 的 ResponseWriter
type ginStreamWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (w *ginStreamWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *ginStreamWriter) WriteString(data string) (int, error) {
	return io.WriteString(w.writer, data)
}

func (w *ginStreamWriter) Flush() error {
	w.flusher.Flush()
	return nil
}

// selectModel 根据配置的策略选择模型
func (s *Server) selectModel(group *config.ModelGroupConfig) config.ModelRef {
	models := group.Models
	modelCount := len(models)

	switch group.Strategy {
	case "round-robin":
		s.roundRobinMutex.Lock()
		defer s.roundRobinMutex.Unlock()
		idx := s.roundRobinIndex[group.ID]
		s.roundRobinIndex[group.ID] = (idx + 1) % modelCount
		return models[idx]

	case "random":
		idx := rand.Intn(modelCount)
		return models[idx]

	case "sequential":
		// sequential 策略：总是选择第一个可用模型
		// 如果失败，会在重试逻辑中尝试下一个
		return models[0]

	default:
		// 默认使用第一个模型
		return models[0]
	}
}

// validateModelGroup 验证模型组配置
func (s *Server) validateModelGroup(groupName string) (*config.ModelGroupConfig, error) {
	if groupName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	group := s.config.GetGroupByName(groupName)
	if group == nil {
		return nil, fmt.Errorf("model group '%s' not found", groupName)
	}
	if !group.Enabled {
		return nil, fmt.Errorf("model group '%s' is disabled", groupName)
	}
	if len(group.Models) == 0 {
		return nil, fmt.Errorf("no available models in group '%s'", groupName)
	}
	return group, nil
}

func (s *Server) acquireRateLimit(group *config.ModelGroupConfig, estimatedTokens int) (func(actualTokens int), error) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	state := s.getOrCreateRateLimitStateLocked(group.ID)

	if group.MaxConcurrency > 0 && state.Active >= group.MaxConcurrency {
		return nil, fmt.Errorf("max concurrency exceeded for group '%s'", group.Name)
	}
	if group.DailyLimitMaxRequests > 0 && state.Requests >= group.DailyLimitMaxRequests {
		return nil, fmt.Errorf("daily request limit exceeded for group '%s'", group.Name)
	}
	if group.DailyLimitMaxTokens > 0 && estimatedTokens > 0 && state.Tokens+estimatedTokens > group.DailyLimitMaxTokens {
		return nil, fmt.Errorf("daily token limit exceeded for group '%s'", group.Name)
	}

	state.Active++
	state.Requests++
	if estimatedTokens > 0 {
		state.Tokens += estimatedTokens
	}

	return func(actualTokens int) {
		s.rateLimitMu.Lock()
		defer s.rateLimitMu.Unlock()

		current := s.getOrCreateRateLimitStateLocked(group.ID)
		if current.Active > 0 {
			current.Active--
		}

		if actualTokens > 0 {
			current.Tokens += actualTokens - estimatedTokens
			if current.Tokens < 0 {
				current.Tokens = 0
			}
		}
	}, nil
}

func (s *Server) adjustTokenUsage(groupID string, estimatedTokens, actualTokens int) {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	state := s.getOrCreateRateLimitStateLocked(groupID)
	state.Tokens += actualTokens - estimatedTokens
	if state.Tokens < 0 {
		state.Tokens = 0
	}
}

func (s *Server) getOrCreateRateLimitStateLocked(groupID string) *rateLimitState {
	today := time.Now().Format("2006-01-02")
	state, ok := s.rateLimits[groupID]
	if !ok {
		state = &rateLimitState{Date: today}
		s.rateLimits[groupID] = state
	}
	if state.Date != today {
		state.Date = today
		state.Requests = 0
		state.Tokens = 0
		state.Active = 0
	}
	return state
}

func estimateUnifiedRequestTokens(req *relay.UnifiedRequest) int {
	return estimateUnifiedRequestInputTokens(req) + estimateUnifiedRequestOutputTokens(req.MaxTokens, req.MaxCompletionTokens)
}

func estimateUnifiedRequestInputTokens(req *relay.UnifiedRequest) int {
	totalChars := 0
	for _, msg := range req.Messages {
		totalChars += estimateContentChars(msg.Content)
	}
	inputTokens := (totalChars + 3) / 4
	if inputTokens < 0 {
		return 0
	}
	return inputTokens
}

func estimateUnifiedRequestOutputTokens(requestedMaxTokens int, requestedMaxCompletionTokens int) int {
	outputBudget := requestedMaxCompletionTokens
	if outputBudget == 0 {
		outputBudget = requestedMaxTokens
	}
	if outputBudget < 0 {
		return 0
	}
	return outputBudget
}

func validateOutboundBaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("missing host")
	}
	if parsed.User != nil {
		return fmt.Errorf("userinfo is not allowed in baseUrl")
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("missing hostname")
	}
	if strings.EqualFold(hostname, "localhost") {
		return fmt.Errorf("loopback host is not allowed")
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("dns resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("hostname resolved to no addresses")
	}

	for _, ip := range ips {
		if isPrivateOrRestrictedIP(ip) {
			return fmt.Errorf("resolved IP %s is private or restricted", ip.String())
		}
	}

	return nil
}

func isPrivateOrRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return true
	}

	if v4 := ip.To4(); v4 != nil {
		// 169.254.0.0/16
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
		// 100.64.0.0/10 carrier-grade NAT
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		// 0.0.0.0/8
		if v4[0] == 0 {
			return true
		}
		return false
	}

	// IPv6 unique local fc00::/7
	if len(ip) == net.IPv6len {
		if (ip[0] & 0xfe) == 0xfc {
			return true
		}
		// fe80::/10 link local
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
			return true
		}
	}

	return false
}

func (s *Server) listModels(c *gin.Context) {
	groups := s.config.GetGroups()

	// 返回模型组名称作为模型 ID
	// 客户端看到的是模型组名称，请求时使用模型组名称
	// 后端根据配置的轮询策略将请求转发给组内的具体模型
	var models []gin.H
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		models = append(models, gin.H{
			"id":       group.Name, // 使用模型组名称
			"object":   "model",
			"created":  0,
			"owned_by": "elysia-api",
		})
	}

	c.JSON(200, gin.H{
		"object": "list",
		"data":   models,
	})
}

func (s *Server) listGeminiModels(c *gin.Context) {
	groups := s.config.GetGroups()

	// 返回 Gemini 原生格式：{ models: [{ name: "models/GROUP_NAME", ... }] }
	type geminiModel struct {
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		Description                string   `json:"description"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	}

	var models []geminiModel
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		inputLimit := group.MaxTokens
		if inputLimit == 0 {
			inputLimit = 1048576
		}
		models = append(models, geminiModel{
			Name:                       "models/" + group.Name,
			DisplayName:                group.Name,
			Description:                "elysia-api model group",
			InputTokenLimit:            inputLimit,
			OutputTokenLimit:           8192,
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
		})
	}

	c.JSON(200, gin.H{"models": models})
}

func (s *Server) countTokens(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "Failed to read request body"})
		return
	}

	unifiedReq, err := relay.ConvertToUnified(bodyBytes, relay.FormatClaude)
	if err != nil {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	totalChars := 0
	for _, msg := range unifiedReq.Messages {
		totalChars += estimateContentChars(msg.Content)
	}

	// 粗略估算：中英文混合场景下按 1 token ≈ 4 chars 估算
	inputTokens := (totalChars + 3) / 4
	if inputTokens < 0 {
		inputTokens = 0
	}

	c.JSON(200, gin.H{
		"input_tokens": inputTokens,
	})
}

func estimateContentChars(content interface{}) int {
	if content == nil {
		return 0
	}

	switch v := content.(type) {
	case string:
		return len([]rune(v))
	case []interface{}:
		total := 0
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				itemType, _ := itemMap["type"].(string)
				if itemType == "text" {
					if text, ok := itemMap["text"].(string); ok {
						total += len([]rune(text))
					}
				}
			}
		}
		return total
	default:
		return len([]rune(fmt.Sprintf("%v", content)))
	}
}

func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func (s *Server) ListenAndServe() error {
	s.setupRoutes()

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	log.Printf("Starting server on %s", addr)

	return s.engine.Run(addr)
}

// RegisterHeartbeatHandler 注册心跳处理器
func (s *Server) RegisterHeartbeatHandler(handler http.HandlerFunc) {
	s.engine.GET("/__heartbeat", s.heartbeatLoopbackOnly(handler))
}
