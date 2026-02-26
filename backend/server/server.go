package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
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

	return &Server{
		config:          cfg,
		engine:          engine,
		openaiAdapter:   relay.NewOpenAIAdapter(),
		roundRobinIndex: make(map[string]int),
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
	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		c.JSON(400, gin.H{"error": "Failed to read request body"})
		return
	}

	log.Printf("=== Incoming Request (raw) ===")
	log.Printf("%s", string(bodyBytes))

	// 检测请求格式
	inputFormat := relay.DetectInputFormat(bodyBytes)
	log.Printf("Detected input format: %s", inputFormat)

	// 转换为统一格式
	unifiedReq, err := relay.ConvertToUnified(bodyBytes)
	if err != nil {
		log.Printf("Error converting request: %v", err)
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	log.Printf("=== Unified Request ===")
	if unifiedReqJSON, err := relay.MarshalUnifiedRequest(unifiedReq); err == nil {
		log.Printf("%s", string(unifiedReqJSON))
	}

	// 获取模型名称（实际是模型组名称）
	modelName := unifiedReq.Model
	if modelName == "" {
		c.JSON(400, gin.H{"error": "Model name is required"})
		return
	}

	// 根据模型组名称查找配置
	group := s.config.GetGroupByName(modelName)
	if group == nil {
		c.JSON(404, gin.H{"error": fmt.Sprintf("Model group '%s' not found", modelName)})
		return
	}

	// 检查模型组是否启用
	if !group.Enabled {
		c.JSON(403, gin.H{"error": fmt.Sprintf("Model group '%s' is disabled", modelName)})
		return
	}

	// 检查是否有可用模型
	if len(group.Models) == 0 {
		c.JSON(503, gin.H{"error": fmt.Sprintf("No available models in group '%s'", modelName)})
		return
	}

	// 根据策略选择具体模型
	selectedModel := s.selectModel(group)
	log.Printf("Group '%s' -> Selected model: %s (platform: %s, strategy: %s)", group.Name, selectedModel.Name, selectedModel.Platform, group.Strategy)

	// 更新模型名称
	unifiedReq.Model = selectedModel.Name

	// 检测目标平台
	targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
	log.Printf("Target platform: %s", targetPlatform)

	// 从统一格式转换为目标平台格式
	targetBody, err := relay.ConvertFromUnified(unifiedReq, targetPlatform)
	if err != nil {
		log.Printf("Error converting to target format: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	log.Printf("=== Outgoing Request to %s ===", selectedModel.BaseURL)
	log.Printf("%s", string(targetBody))

	// 检查是否为流式请求
	isStream := relay.IsStreamRequest(targetBody)

	if isStream {
		// 流式请求处理
		s.handleStreamRequest(c, selectedModel, targetBody)
	} else {
		// 非流式请求处理
		s.handleNormalRequest(c, selectedModel, targetBody)
	}
}

func (s *Server) handleNormalRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte) {
	// 转发请求到选定的模型
	resp, err := s.openaiAdapter.SendRequestRaw(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
	if err != nil {
		log.Printf("Error forwarding request: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward request: %v", err)})
		return
	}

	log.Printf("=== Response ===")
	if respJSON, err := relay.MarshalResponse(resp); err == nil {
		log.Printf("%s", string(respJSON))
	}

	// 返回模型的响应
	c.JSON(200, resp)
}

func (s *Server) handleStreamRequest(c *gin.Context, selectedModel config.ModelRef, targetBody []byte) {
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
