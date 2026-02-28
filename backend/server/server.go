package server

import (
	"bufio"
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
		v1.GET("/models", s.listModels)
	}

	s.engine.GET("/health", s.healthCheck)
}

func (s *Server) chatCompletions(c *gin.Context) {
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

	// 检测请求格式
	inputFormat := relay.DetectInputFormat(bodyBytes)
	s.logVerbose("Detected input format: %s", inputFormat)

	// 转换为统一格式
	unifiedReq, err := relay.ConvertToUnified(bodyBytes)
	if err != nil {
		log.Printf("Error converting request: %v", err)
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
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
	isStream := relay.IsStreamRequest(targetBody)

	if isStream {
		// 流式请求处理
		s.handleStreamRequest(c, selectedModel, targetBody, startTime)
	} else {
		// 非流式请求处理
		s.handleNormalRequest(c, selectedModel, targetBody, startTime)
	}
}

func (s *Server) handleNormalRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte, startTime time.Time) {
	// 转发请求到选定的模型
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

	// 记录请求耗时
	duration := time.Since(startTime)
	s.logDebug("Request completed in %dms", duration.Milliseconds())

	// 返回模型的响应
	c.JSON(200, resp)
}

func (s *Server) handleStreamRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte, startTime time.Time) {
	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// 发送流式请求
	resp, err := s.openaiAdapter.SendRequestStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
	if err != nil {
		log.Printf("Error forwarding stream request: %v", err)
		c.SSEvent("error", fmt.Sprintf("Failed to forward request: %v", err))
		return
	}
	defer resp.Body.Close()

	// 转发流式响应
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported")
		c.JSON(500, gin.H{"error": "Streaming not supported"})
		return
	}

	// 使用 bufio 逐行读取并转发
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// 直接转发 SSE 行
		c.Writer.Write([]byte(line + "\n\n"))
		flusher.Flush()
	}

	// 记录请求耗时
	duration := time.Since(startTime)
	s.logDebug("Stream request completed in %dms", duration.Milliseconds())

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading stream: %v", err)
	}
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
		rand.Seed(time.Now().UnixNano())
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
		models = append(models, gin.H{
			"id":       group.Name,  // 使用模型组名称
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
