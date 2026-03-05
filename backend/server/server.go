package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

type Server struct {
	config        *config.Config
	engine        *gin.Engine
	openaiAdapter *relay.OpenAIAdapter
	claudeAdapter *relay.ClaudeAdapter
	geminiAdapter *relay.GeminiAdapter
	// 轮询状态跟踪：模型组ID -> 当前模型索引
	roundRobinIndex map[string]int
	roundRobinMutex sync.Mutex
}

func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.Default()

	// 获取 HTTP 超时配置，默认 120 秒
	httpTimeout := time.Duration(cfg.HTTPTimeout) * time.Second
	if cfg.HTTPTimeout == 0 {
		httpTimeout = 0 // 0 表示不限制
	}

	return &Server{
		config:          cfg,
		engine:          engine,
		openaiAdapter:   relay.NewOpenAIAdapter(httpTimeout),
		claudeAdapter:   relay.NewClaudeAdapter(httpTimeout),
		geminiAdapter:   relay.NewGeminiAdapter(httpTimeout),
		roundRobinIndex: make(map[string]int),
	}
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

func (s *Server) setupRoutes() {
	v1 := s.engine.Group("/v1")
	{
		v1.POST("/chat/completions", s.chatCompletions)
		v1.POST("/messages", s.chatCompletions)          // Claude 原生格式入口
		v1.POST("/messages/count_tokens", s.countTokens) // Claude 兼容 token 统计端点
		v1.GET("/models", s.listModels)
	}

	// Gemini 原生 API 兼容路由
	// /v1beta/models/MODEL:generateContent 和 /v1beta/models/MODEL:streamGenerateContent
	// gin 不支持参数内含冒号，用通配符捕获整段路径
	v1beta := s.engine.Group("/v1beta")
	{
		v1beta.GET("/models", s.listGeminiModels)
		v1beta.POST("/models/*action", s.chatCompletions)
	}

	s.engine.GET("/health", s.healthCheck)
}

func (s *Server) chatCompletions(c *gin.Context) {
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

	s.logVerbose("=== Incoming Request (raw) ===")
	s.logVerbose("%s", string(bodyBytes))

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
	s.logVerbose("Input format (by path): %s", inputFormat)

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

	s.logVerbose("=== Unified Request ===")
	if unifiedReqJSON, err := relay.MarshalUnifiedRequest(unifiedReq); err == nil {
		s.logVerbose("%s", string(unifiedReqJSON))
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
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	// 根据策略选择具体模型
	selectedModel := s.selectModel(group)
	s.logDebug("Request model group: '%s', selected: %s", group.Name, selectedModel.Name)

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

	// 检测目标平台
	targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
	s.logVerbose("Target platform: %s", targetPlatform)

	// 从统一格式转换为目标平台格式
	targetBody, err := relay.ConvertFromUnified(unifiedReq, targetPlatform)
	if err != nil {
		log.Printf("Error converting to target format: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	s.logVerbose("=== Outgoing Request to %s ===", selectedModel.BaseURL)
	s.logVerbose("%s", string(targetBody))

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
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to prepare stream request: %v", streamBodyErr)})
			return
		}
		s.logVerbose("=== Outgoing Stream Request (patched) ===")
		s.logVerbose("%s", string(targetBody))
	}

	if isStream {
		// 流式请求处理
		s.handleStreamRequest(c, selectedModel, targetBody, targetPlatform, inputFormat, startTime)
	} else {
		// 非流式请求处理
		s.handleNormalRequest(c, selectedModel, targetBody, targetPlatform, inputFormat, startTime)
	}
}

func (s *Server) handleNormalRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time) {
	// 设计原则：
	// 1) 先按 targetPlatform 获取并解析上游响应
	// 2) 再按 inputFormat 渲染客户端响应
	// 这样输入协议与下游平台彻底解耦，避免协议错配。
	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, false)
		if err != nil {
			log.Printf("Error forwarding Claude request: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

		var claudeResp relay.ClaudeResponse
		if err := readJSON(httpResp, &claudeResp); err != nil {
			log.Printf("Error parsing Claude response: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to parse response: %v", err)})
			return
		}

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
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

		var geminiResp relay.GeminiResponse
		if err := readJSON(httpResp, &geminiResp); err != nil {
			log.Printf("Error parsing Gemini response: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to parse response: %v", err)})
			return
		}

		oaiResp := relay.ConvertGeminiResponseToOpenAI(&geminiResp)
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
		resp, err := s.openaiAdapter.SendRequestRaw(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding request: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
			return
		}

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

func (s *Server) handleStreamRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time) {
	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported")
		c.JSON(500, gin.H{"error": "Streaming not supported"})
		return
	}
	writer := &ginStreamWriter{writer: c.Writer, flusher: flusher}

	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
		if err != nil {
			log.Printf("Error forwarding Claude stream request: %v", err)
			writeStreamForwardError(c, inputFormat, err)
			return
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

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
			writeStreamForwardError(c, inputFormat, err)
			return
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			c.Data(httpResp.StatusCode, "application/json", respBody)
			return
		}

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
			writeStreamForwardError(c, inputFormat, err)
			return
		}

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
		if _, exists := req["stream_options"]; !exists {
			req["stream_options"] = map[string]interface{}{
				"include_usage": true,
			}
		}
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
	s.engine.GET("/__heartbeat", func(c *gin.Context) {
		handler(c.Writer, c.Request)
	})
}
