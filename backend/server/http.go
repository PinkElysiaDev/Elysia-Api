package server

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/webui"
	"github.com/gin-gonic/gin"
)

func (s *Server) setupRoutes() {
	if s.config.GetMaxBodyBytes() > 0 {
		s.engine.Use(func(c *gin.Context) {
			if !s.pinProtocolRequest(c) {
				c.Abort()
				return
			}
			limit := int64(protocol.RequestBodyLimit(c.Request.Context()))
			if protocolPreviewPath(c.Request.URL.Path) {
				limit = int64(protocol.PreviewEnvelopeLimit(c.Request.Context()))
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			c.Next()
		})
	}
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
	s.engine.Any("/gateway/:protocolId/*path", s.authMiddleware(), s.gatewayProtocol)

	s.mountWebUI()
	if s.config.EnablePprof {
		debug := s.engine.Group("/debug/pprof")
		debug.Use(s.dashboardAuthMiddleware())
		debug.GET("/", gin.WrapF(pprof.Index))
		debug.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		debug.GET("/profile", gin.WrapF(pprof.Profile))
		debug.GET("/symbol", gin.WrapF(pprof.Symbol))
		debug.GET("/trace", gin.WrapF(pprof.Trace))
		debug.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		debug.GET("/block", gin.WrapH(pprof.Handler("block")))
		debug.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		debug.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		debug.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		debug.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}

	admin := s.engine.Group("/api/admin")
	admin.Use(s.dashboardAuthMiddleware())
	{
		s.setupAdminRoutes(admin)
		s.setupAgentRoutes(admin)
	}

	// AI 助手远程面：REST + MCP + A2A，共用 Bearer API key（agent 作用域）
	// 鉴权与 config.agentRemote 总开关。
	remote := s.engine.Group("/api")
	remote.Use(s.agentRemoteGate(), s.agentRemoteAuth())
	{
		s.setupAgentRemoteRoutes(remote)
	}
	s.engine.Any("/mcp", s.agentRemoteGate(), s.agentRemoteAuth(), s.handleMCP)
	s.engine.GET("/.well-known/agent-card.json", s.agentRemoteGate(), s.handleAgentCard)
	s.engine.POST("/a2a", s.agentRemoteGate(), s.agentRemoteAuth(), s.handleA2A)

	s.engine.GET("/health", s.healthCheck)
	s.engine.POST("/__reload", s.loopbackOnly(s.reloadConfig))
	s.engine.POST("/__shutdown", s.loopbackOnly(s.shutdown))
}

// mountWebUI 在 /ui 提供控制台静态资源，优先级：
//  1. 配置了 webuiDir 且目录存在 → 用外部目录（开发期 / 自定义覆盖）；
//  2. 否则使用内嵌资源（//go:embed，开箱即用、零配置）；
//  3. 两者都没有 → 记日志说明 WebUI 未启用，不静默 404。
func (s *Server) mountWebUI() {
	if dir := strings.TrimSpace(s.config.WebUIDir); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			s.engine.Static("/ui", dir)
			s.logEvent("info").Str("path", dir).Msg("WebUI mounted from external directory")
			return
		}
		s.logEvent("warn").Str("path", dir).Msg("configured webuiDir not found; falling back to embedded WebUI")
	}

	if sub, ok := webui.FS(); ok {
		// hashed 文件名的静态资源可永久强缓存；其余（index.html）必须每次
		// 重新校验，避免升级二进制后旧 index 引用新 hash 资源 404 白屏。
		// 子目录一律 404，阻止 http.FileServer 渲染目录列表页（文件名枚举）。
		ui := s.engine.Group("/ui", func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/ui/assets/") {
				c.Header("Cache-Control", CacheHeaderImmutable)
			} else {
				c.Header("Cache-Control", "no-cache")
			}
		})
		ui.StaticFS("/", http.FS(noDirectoryFS{inner: sub}))
		s.logEvent("info").Msg("WebUI mounted from embedded assets at /ui")
		return
	}

	s.logEvent("warn").Msg("WebUI is not available; /ui is disabled")
}

// noDirectoryFS 隐藏子目录：http.FileServer 对无 index.html 的目录会渲染
// 目录列表页，泄露资源文件名；根目录（含 index.html）保持正常服务。
type noDirectoryFS struct {
	inner fs.FS
}

func (n noDirectoryFS) Open(name string) (fs.File, error) {
	f, err := n.inner.Open(name)
	if err != nil {
		return nil, err
	}
	if stat, statErr := f.Stat(); statErr == nil && stat.IsDir() && name != "." {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

func (s *Server) reloadConfig(c *gin.Context) {
	s.configUpdateMu.Lock()
	defer s.configUpdateMu.Unlock()
	oldServer := s.config.GetServer()
	oldHost := oldServer.Host
	oldPort := oldServer.Port
	oldHTTPTimeout := s.config.GetHTTPTimeout()
	oldDatabase, oldLogDatabase := s.config.GetDatabasePath(), s.config.GetLogDatabasePath()
	oldUsage, oldSystem := s.config.GetUsageLogConfig(), s.config.GetSystemLogConfig()

	if err := s.config.Reload(); err != nil {
		s.logSystemEvent("warn", "config reload failed", map[string]any{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{
			"reloaded": false,
			"error":    err.Error(),
		})
		return
	}
	s.Logger().Configure(s.config.GetLoggingOptions())
	if oldDatabase != s.config.GetDatabasePath() || oldLogDatabase != s.config.GetLogDatabasePath() {
		s.markRestartRequired()
	}
	if s.usageRetention != nil && retentionTightened(oldUsage, s.config.GetUsageLogConfig(), oldSystem, s.config.GetSystemLogConfig()) {
		s.usageRetention.triggerAsync()
	}
	s.logSystemEvent("info", "config reloaded", nil)

	newServer := s.config.GetServer()
	serverChanged := oldHost != newServer.Host || oldPort != newServer.Port
	// httpTimeout 属于 adapter 客户端参数,config.json 路径的热重载也要下发
	//(此前只有管理端 PUT 会 SetTimeout,文件路径改超时静默不生效直到重启)。
	if timeout := s.config.GetHTTPTimeout(); timeout != oldHTTPTimeout {
		s.logEvent("info").Int("old_timeout", oldHTTPTimeout).Int("timeout", timeout).Msg("HTTP timeout hot-reloaded")
		duration := time.Duration(timeout) * time.Second
		s.protocolTransport.SetTimeout(duration)
	}
	// 配置热更新后失效路由缓存，下次请求按新配置重建（借鉴 SyncOptions）。
	s.invalidateRouteCache()
	// 出站禁止 IP 段可能随配置变更，同步到 outbound 包（即时生效）。
	// 自定义协议存 SQLite，不随 config.json 热重载：管理端点写入时即时同步。
	s.syncOutboundPolicy()
	if serverChanged {
		s.markRestartRequired()
		s.logEvent("info").Msgf(
			"Config hot-reloaded successfully, but server listen address change requires restart (old=%s:%d new=%s:%d)",
			oldHost,
			oldPort,
			newServer.Host,
			newServer.Port,
		)
	} else {
		s.logEvent("info").Msg("config hot-reloaded successfully")
	}

	c.JSON(http.StatusOK, gin.H{
		"reloaded":                     true,
		"debugMode":                    s.config.IsDebugMode(),
		"verboseLog":                   s.config.IsVerboseLog(),
		"serverChangedRequiresRestart": serverChanged,
		"server": gin.H{
			"oldHost": oldHost,
			"oldPort": oldPort,
			"newHost": newServer.Host,
			"newPort": newServer.Port,
		},
	})
}

func (s *Server) chatCompletions(c *gin.Context) { s.serveVersionedPublicIngress(c) }
func (s *Server) responses(c *gin.Context)       { s.serveVersionedPublicIngress(c) }

// validateModelGroup 验证模型组配置,失败返回按稳定错误分类组织的核心错误
// (各线制的状态码/type/code 由分类派生;消息不暴露内部「组」概念)。
func (s *Server) validateModelGroup(groupName string) (*config.ModelGroupConfig, *builtin.GatewayError) {
	if groupName == "" {
		return nil, &builtin.GatewayError{Class: builtin.ErrorClassInvalidRequest, Message: "model name is required"}
	}
	group := s.findGroupByName(groupName)
	if group == nil || len(group.Models) == 0 {
		// 组不存在与组内无可用模型对客户端同义:该模型不可用。
		// 4xx 让 SDK/Codex 停止自动重试并正确提示。
		return nil, &builtin.GatewayError{
			Class:   builtin.ErrorClassModelNotFound,
			Message: fmt.Sprintf("The model '%s' does not exist or is not available", groupName),
		}
	}
	if !group.Enabled {
		return nil, &builtin.GatewayError{
			Class:   builtin.ErrorClassPermission,
			Message: fmt.Sprintf("The model '%s' is disabled by the administrator", groupName),
		}
	}
	return group, nil
}

func (s *Server) listModels(c *gin.Context) {
	groups := s.getGroups()

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
	groups := s.getGroups()

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
			inputLimit = geminiDefaultInputTokenLimit
		}
		models = append(models, geminiModel{
			Name:                       "models/" + group.Name,
			DisplayName:                group.Name,
			Description:                "elysia-api model group",
			InputTokenLimit:            inputLimit,
			OutputTokenLimit:           geminiDefaultOutputTokenLimit,
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
		})
	}

	c.JSON(200, gin.H{"models": models})
}

func (s *Server) countTokens(c *gin.Context) {
	if !s.requireProtocolRuntime(c) {
		return
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	compiled, exists := service.View().Pin(publicProtocolIDs[builtin.FormatClaude])
	if !exists {
		respondFail(c, http.StatusServiceUnavailable, "inactive_protocol", "Anthropic protocol is not enabled")
		return
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, compiled.RequestLimits(c.Request.Context()).BufferBytes)
	if err != nil {
		writeProtocolError(c, builtin.FormatClaude, &builtin.GatewayError{Class: builtin.ErrorClassInvalidRequest, Message: err.Error()})
		return
	}
	request, err := compiled.DecodeRequest(c.Request.Context(), body, protocol.EvaluationContext{})
	if err != nil {
		writeProtocolError(c, builtin.FormatClaude, &builtin.GatewayError{Class: builtin.ErrorClassInvalidRequest, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": s.estimateProtocolInputTokens(request)})
}

func (s *Server) healthCheck(c *gin.Context) {
	// 公开（无鉴权）健康端点，供负载均衡 / k8s 探针使用。
	// 深入探测数据库依赖：store 不可用或 Ping 失败时返回 503，
	// 这样探针能据此摘除不健康实例。
	dbOK, logDBOK := false, false
	if s.store != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		dbOK = s.store.Ping(ctx) == nil
		logDBOK = s.store.LogStore.Ping(ctx) == nil
	}

	status := "ok"
	code := http.StatusOK
	if !dbOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, gin.H{"status": status, "database": dbOK, "logDatabase": logDBOK, "version": AppVersion, "commit": AppCommit})
}
