package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/elysia-api/backend/webui"
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
	store        *storage.Store

	// 异步 usage 写入：store 模式下，请求路径只把记录投递到 buffer channel，
	// 由单个 writer goroutine 落库，避免请求在 SQLite 写入（单连接串行）上阻塞。
	usageQueue    chan *usageRecord
	usageWriterWG sync.WaitGroup

	// 渠道亲和性：token+group → 上次成功模型的短 TTL 粘连映射。
	affinity *affinityCache

	// 可选的后台健康检测器（config.HealthCheck.Enabled 控制）。
	healthChecker *healthChecker

	// httpServer 持有底层 http.Server 引用，供 /__shutdown 优雅关停使用。
	httpServer *http.Server

	// 路由缓存：把 groups+models 装配结果与 tokens 载入内存，
	// 让请求热路径无需每次查 SQLite（消除 N+1 + 单连接串行瓶颈）。
	// 借鉴 new-api 的 *_cache.go + SyncOptions：读走内存，写后失效。
	routeCacheMu     sync.RWMutex
	cachedGroups     []config.ModelGroupConfig
	cachedTokens     map[string]config.AccessToken
	routeCacheLoaded bool

	// skipOutboundValidation 仅供测试使用：跳过 SSRF 出站校验，
	// 以便用 httptest 的 127.0.0.1 上游做端到端转发/故障转移测试。
	// 生产路径恒为 false。
	skipOutboundValidation bool
}

func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	// gin.Logger 会为每个请求打一行访问日志。正常运行只保留 Recovery，
	// 仅在调试模式下启用访问日志。
	if cfg.DebugMode {
		engine.Use(gin.Logger())
	}

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
		affinity:        newAffinityCache(),
	}
	if store, err := storage.OpenWithKey(cfg.DatabasePath, cfg.GetDBEncryptionKey()); err != nil {
		log.Printf("failed to open sqlite store: %v", err)
	} else {
		server.store = store
		if err := server.importLegacyConfig(); err != nil {
			log.Printf("failed to import legacy config into sqlite: %v", err)
		}
	}
	server.syncRelaySSRFPolicy()
	server.syncCustomProtocols()
	return server
}

// syncRelaySSRFPolicy 把 SSRF 相关运行时配置下发给 relay 包的包级开关。
// 在启动、热重载、admin 改配置后调用，确保连接时校验与预校验即时反映配置。
func (s *Server) syncRelaySSRFPolicy() {
	relay.SetAllowFakeIPRanges(s.config.IsFakeIPOutboundAllowed())
}

// logDebug 仅在调试模式或 LogLevel=debug 时输出基本信息（模型组、选中模型、耗时）
func (s *Server) logDebug(format string, args ...interface{}) {
	if s.config.IsDebugMode() || s.currentLogThreshold() <= logLevelPriority["debug"] {
		log.Printf("[debug] "+format, args...)
	}
}

// logVerbose 仅在详细日志模式下输出完整请求/响应结构
func (s *Server) logVerbose(format string, args ...interface{}) {
	if s.config.IsVerboseLog() {
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

func (s *Server) setupRoutes() {
	if s.config.MaxBodyBytes > 0 {
		s.engine.Use(func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.config.MaxBodyBytes)
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

	s.engine.GET("/usage", s.usageDashboard)
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

	usage := s.engine.Group("/__usage")
	usage.Use(s.dashboardAuthMiddleware())
	{
		usage.GET("/stats", s.usageStats)
		usage.GET("/logs", s.usageLogs)
		usage.GET("/logs/:id", s.usageLogDetail)
		usage.POST("/reset", s.resetUsage)
	}

	admin := s.engine.Group("/api/admin")
	admin.Use(s.dashboardAuthMiddleware())
	{
		s.setupAdminRoutes(admin)
	}

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
			log.Printf("WebUI mounted from external directory: %s", dir)
			return
		}
		log.Printf("configured webuiDir %q not found, falling back to embedded WebUI", dir)
	}

	if sub, ok := webui.FS(); ok {
		s.engine.StaticFS("/ui", http.FS(sub))
		log.Printf("WebUI mounted from embedded assets at /ui")
		return
	}

	log.Printf("WebUI is not available (no embedded assets and no valid webuiDir); /ui is disabled")
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		accessToken, ok := s.findAccessToken(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}
		c.Set("elysiaKeyName", accessToken.Name)
		c.Set("elysiaKeyHash", shortTokenHash(token))
		c.Set("elysiaAllowedGroups", accessToken.AllowedGroups)
		c.Next()
	}
}

func (s *Server) dashboardAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		if !s.config.IsValidPanelAccessToken(token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "panel access token is not configured or invalid",
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

	// Check cookie for panel access token.
	// 前端写入 cookie 时用了 encodeURIComponent，而 Go 的 r.Cookie() 不会自动解码，
	// 这里手动 url.QueryUnescape 还原，保证含特殊字符的 token 也能匹配。
	if cookie, err := r.Cookie("panel_access_token"); err == nil {
		if decoded, derr := url.QueryUnescape(cookie.Value); derr == nil {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(cookie.Value)
	}

	return ""
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
	oldServer := s.config.GetServer()
	oldHost := oldServer.Host
	oldPort := oldServer.Port

	if err := s.config.Reload(); err != nil {
		log.Printf("Config reload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"reloaded": false,
			"error":    err.Error(),
		})
		return
	}

	newServer := s.config.GetServer()
	serverChanged := oldHost != newServer.Host || oldPort != newServer.Port
	// 配置热更新后失效路由缓存，下次请求按新配置重建（借鉴 SyncOptions）。
	s.invalidateRouteCache()
	// SSRF 放行策略可能随配置变更，同步到 relay 包级开关（即时生效）。
	s.syncRelaySSRFPolicy()
	s.syncCustomProtocols()
	if serverChanged {
		log.Printf(
			"Config hot-reloaded successfully, but server listen address change requires restart (old=%s:%d new=%s:%d)",
			oldHost,
			oldPort,
			newServer.Host,
			newServer.Port,
		)
	} else {
		log.Printf("Config hot-reloaded successfully")
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

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func geminiModelFromAction(action string) string {
	modelPart := strings.TrimPrefix(strings.TrimSpace(action), "/")
	modelPart = strings.TrimPrefix(modelPart, "models/")
	if idx := strings.LastIndex(modelPart, ":"); idx != -1 {
		modelPart = modelPart[:idx]
	}
	return strings.TrimSpace(modelPart)
}

func (s *Server) chatCompletions(c *gin.Context) {
	s.logVerbose("[REQUEST ENTER] path=%s method=%s remote=%s contentType=%s", c.Request.URL.Path, c.Request.Method, c.Request.RemoteAddr, c.Request.Header.Get("Content-Type"))
	// 生产转换路径统一为 Maheshvara：
	//   非流式：client wire -> MaheshvaraRequest -> target wire；provider response -> MaheshvaraResponse -> client wire。
	//   流式：provider SSE -> source decoder -> MaheshvaraStreamEvent -> target renderer -> client SSE。
	// 协议同源且请求未被过滤时，直接绕过 Maheshvara 往返、零转换透传。
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
	installDownstreamCapture(c, record)
	s.logVerbose("[Input Format] %s", inputFormat)

	// 转换为 Maheshvara 核心请求。
	urlModel := ""
	if inputFormat == relay.FormatGemini {
		urlModel = geminiModelFromAction(c.Param("action"))
	}
	canonicalReq, _, canonicalErr := relay.ConvertRequestToCanonical(bodyBytes, inputFormat, urlModel)
	if canonicalErr != nil {
		log.Printf("Error converting request to Maheshvara: %v", canonicalErr)
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request to Maheshvara: %v", canonicalErr)})
		return
	}

	// Gemini 原生路径 /v1beta/models/MODEL:generateContent 中模型名在 URL 里
	// 若请求体没有 model 字段，从路径参数提取
	if canonicalReq.Model == "" {
		if action := c.Param("action"); action != "" {
			// action 形如 /gemini-2.0-flash:generateContent
			modelPart := geminiModelFromAction(action)
			if idx := strings.LastIndex(modelPart, ":"); idx != -1 {
				modelPart = modelPart[:idx]
			}
			canonicalReq.Model = modelPart
		}
	}
	if canonicalJSON, err := json.Marshal(canonicalReq); err == nil {
		s.logVerbose("[Maheshvara Request] %s", compactLogJSON(canonicalJSON))
	}

	// 模型组级访问权限：先于 validateModelGroup 校验请求的模型组名，
	// 这样即使目标组为空/未配置，越权访问也返回 403（而非泄露组的存在性/状态）。
	if !s.tokenAllowsGroup(c, canonicalReq.Model) {
		s.failRequest(c, record, startTime, http.StatusForbidden, fmt.Sprintf("api key is not allowed to access model group '%s'", canonicalReq.Model))
		return
	}

	// 验证并获取模型组
	group, err := s.validateModelGroup(canonicalReq.Model)
	if err != nil {
		statusCode := 500
		if errMsg := err.Error(); strings.Contains(errMsg, "not found") {
			statusCode = 404
		} else if strings.Contains(errMsg, "disabled") {
			statusCode = 403
		}
		s.failRequest(c, record, startTime, statusCode, err.Error())
		return
	}
	setRecordGroup(record, group)

	// 构建有序候选模型列表，按模型组策略排列。失败时逐个故障转移。
	candidates := s.buildCandidates(group)
	if len(candidates) == 0 {
		s.failRequest(c, record, startTime, http.StatusInternalServerError, fmt.Sprintf("no available models in group '%s'", group.Name))
		return
	}
	// 渠道亲和性：把该 key+group 上次成功的模型提到候选最前（短 TTL 粘连），
	// 提升上游 prompt 缓存命中率。不改变候选集合，故障转移逻辑不受影响。
	if sticky := s.affinity.get(record.KeyHash, group.ID, startTime); sticky != "" {
		candidates = applyAffinity(candidates, sticky)
	}

	// 如果模型组配置了 MaxTokens，覆盖客户端发来的值
	if group.MaxTokens > 0 {
		canonicalReq.MaxOutputTokens = group.MaxTokens
	}
	filtered, filteredParts := filterCanonicalVisionInputsIfNeeded(group, canonicalReq)
	if filtered {
		s.logVerbose("[Maheshvara Vision Filter] group=%s filteredImageParts=%d", group.Name, filteredParts)
		if canonicalJSON, err := json.Marshal(canonicalReq); err == nil {
			s.logVerbose("[Maheshvara Request After Vision Filter] %s", compactLogJSON(canonicalJSON))
		}
	}

	estimatedUsage := estimateCanonicalRequestUsage(canonicalReq, s.config.GetUsageConfig())
	estimatedTokens := estimatedUsage.EstimatedTotalTokens
	record.Usage = usageTokenUsageFromCanonical(estimatedUsage)
	record.UsageDetail = usageDetailFromCanonical(estimatedUsage)
	record.UsageSource = estimatedUsage.Source
	releaseLimiter, err := s.acquireRateLimit(group, estimatedTokens)
	if err != nil {
		s.failRequest(c, record, startTime, http.StatusTooManyRequests, err.Error())
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

		// SSRF 出站校验。校验失败属于配置/安全问题，对单个候选不可恢复，
		// 但其他候选可能合法，因此记为可重试。
		if err := s.validateOutbound(selectedModel.BaseURL); err != nil {
			lastStatus = http.StatusForbidden
			lastErr = fmt.Sprintf("target baseUrl rejected: %v", err)
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				record.StatusCode = lastStatus
				record.Error = lastErr
				c.JSON(http.StatusForbidden, gin.H{"error": lastErr})
				committed = true
			}
			continue
		}

		canonicalReq.Model = selectedModel.Name
		targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
		setRecordModel(record, selectedModel, targetPlatform)
		s.logDebug("Request model group: '%s' attempt %d/%d, selected: %s", group.Name, attempt+1, attempts, selectedModel.Name)

		// 同源透传判定：客户端输入格式与所选上游线路 API 一致（Claude→Anthropic、
		// Gemini→Gemini、OpenAI→OpenAI 系），且本次未因 vision 过滤改写过请求体时，
		// 以原始请求字节直发上游，跳过 Maheshvara 往返——保留尚未纳入核心协议的私有字段
		// （cache_control / thinking / 各类未知扩展）。借鉴 Responses 透传与 new-api
		// 的 should_convert=false 分支。vision 过滤改写了 canonicalReq 而非原始字节，
		// 故 filtered=true 时必须回退到转换路径，否则被过滤的图片会随原始字节漏给上游。
		usePassthrough := !filtered && relay.FormatMatchesPlatform(inputFormat, targetPlatform)

		// 流式意图取自客户端原始请求：OpenAI/Claude 看请求体 stream 字段，
		// Gemini 看 URL action（:streamGenerateContent）。
		isStream := relay.IsStreamRequest(bodyBytes)
		if action := c.Param("action"); strings.Contains(action, ":streamGenerateContent") {
			isStream = true
			canonicalReq.Stream = true
		}

		var targetBody []byte
		var customRequest *relay.CustomProtocolRequestResult
		if usePassthrough {
			// Gemini：model 在 URL 里（adapter 单独接收 selectedModel.Name），原生
			// generateContent 请求体不含顶层 model，故透传时不改写 model（传空），
			// 也不向体内注入 stream（由 URL action 决定）。OpenAI/Claude 则改写 model；
			// OpenAI 兼容线路补 stream_options.include_usage 以拿到 usage chunk。
			passModelName := selectedModel.Name
			addStreamOptions := false
			ensureStream := false
			if targetPlatform == relay.PlatformGemini {
				passModelName = ""
			} else {
				ensureStream = isStream
				addStreamOptions = targetPlatform == relay.PlatformOpenAI || targetPlatform == relay.PlatformDeepSeek || targetPlatform == relay.PlatformAzure
			}
			targetBody, err = relay.PassthroughBody(bodyBytes, passModelName, ensureStream, addStreamOptions)
			if err == nil {
				record.RelayMode = "passthrough"
				// OpenAI 系透传同样补齐缺失的 tool call id：部分客户端重建历史时
				// 会遗漏 tool_calls[].id，直接透传会被严格上游以 missing field id 拒绝。
				if targetPlatform == relay.PlatformOpenAI || targetPlatform == relay.PlatformDeepSeek || targetPlatform == relay.PlatformAzure {
					targetBody, err = relay.NormalizeOpenAIToolCallIDs(targetBody)
				}
			}
		} else if relay.IsCustomPlatform(targetPlatform) {
			customRequest, err = relay.RenderRegisteredCustomProtocolRequest(canonicalReq, relay.CustomProtocolID(targetPlatform))
			if err == nil {
				targetBody = customRequest.Body
			}
			if err == nil {
				record.RelayMode = "transform"
			}
		} else {
			targetFormat, formatErr := relay.TargetFormatForPlatform(targetPlatform)
			if formatErr != nil {
				err = formatErr
			} else {
				targetBody, err = relay.CanonicalToTargetRequest(canonicalReq, targetFormat, nil)
			}
			if err == nil {
				record.RelayMode = "transform"
			}
		}
		if err != nil {
			lastStatus = http.StatusBadRequest
			lastErr = fmt.Sprintf("Failed to build upstream request: %v", err)
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				record.StatusCode = lastStatus
				record.Error = lastErr
				c.JSON(lastStatus, gin.H{"error": lastErr})
				committed = true
			}
			continue
		}
		record.OutgoingBody = sanitizeUsageBody(targetBody)
		s.logVerbose("[Outgoing Request] passthrough=%v baseUrl=%s body=%s", usePassthrough, selectedModel.BaseURL, compactLogJSON(targetBody))

		// 非透传路径仍需为流式补齐 stream 标记（透传已在 PassthroughBody 内处理）。
		if isStream && !usePassthrough && !relay.IsCustomPlatform(targetPlatform) {
			var streamBodyErr error
			targetBody, streamBodyErr = ensureStreamFlagInTargetBody(targetBody, targetPlatform)
			if streamBodyErr != nil {
				lastStatus = http.StatusInternalServerError
				lastErr = fmt.Sprintf("Failed to prepare stream request: %v", streamBodyErr)
				s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
				if isLast {
					record.StatusCode = lastStatus
					record.Error = lastErr
					c.JSON(500, gin.H{"error": lastErr})
					committed = true
				}
				continue
			}
			record.OutgoingBody = sanitizeUsageBody(targetBody)
		}

		var outcome relayOutcome
		if isStream {
			record.Stream = true
			outcome = s.handleStreamRequest(c, group, selectedModel, targetBody, customRequest, targetPlatform, inputFormat, startTime, estimatedTokens, record, isLast)
		} else {
			outcome = s.handleNormalRequest(c, group, selectedModel, targetBody, customRequest, targetPlatform, inputFormat, startTime, estimatedTokens, record, isLast)
		}

		if outcome.committed {
			committed = true
			// 成功（2xx）时记录渠道亲和性，让后续同 key+group 请求优先复用本模型。
			if outcome.statusCode >= 200 && outcome.statusCode < 300 {
				s.affinity.set(record.KeyHash, group.ID, selectedModel.Name, startTime)
			}
			break
		}

		// 未提交：本次失败但可重试。记录失败原因，等待重试间隔后换下一个候选。
		lastStatus = outcome.statusCode
		lastErr = outcome.errMsg
		s.appendRetryEvent(record, attempt, selectedModel.Name, outcome.errMsg)
		if !isLast && group.RetryInterval > 0 {
			time.Sleep(time.Duration(group.RetryInterval) * time.Millisecond)
		}
	}

	// 兜底：理论上最后一次尝试一定会 commit；若因边界情况未 commit，
	// 这里补一个错误响应，避免客户端收到空响应。
	if !committed {
		if lastStatus == 0 {
			lastStatus = http.StatusBadGateway
		}
		record.StatusCode = lastStatus
		record.Error = lastErr
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		c.JSON(http.StatusBadGateway, gin.H{"error": firstNonEmpty(lastErr, "all upstream attempts failed")})
	}
}

func (s *Server) handleNormalRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord, isLast bool) relayOutcome {
	if relay.IsCustomPlatform(targetPlatform) {
		return s.handleCustomNormalRequest(c, group, selectedModel, customRequest, targetPlatform, inputFormat, startTime, record, isLast)
	}
	// failResult 在转发失败时决定是提交错误响应（最后一次尝试或不可重试），
	// 还是返回 committed=false 让上层故障转移到下一个候选模型。
	failResult := func(statusCode int, errMsg string, respBody []byte, contentType string) relayOutcome {
		retryable := shouldRetryStatus(statusCode)
		if isLast || !retryable {
			record.StatusCode = statusCode
			record.Error = errMsg
			if respBody != nil {
				c.Data(statusCode, contentType, respBody)
			} else {
				c.JSON(statusCode, gin.H{"error": errMsg})
			}
			return relayOutcome{committed: true, statusCode: statusCode, errMsg: errMsg}
		}
		return relayOutcome{committed: false, statusCode: statusCode, errMsg: errMsg}
	}

	// 仅在 committed 时记录 usage；未提交（将要重试）时不记录，
	// 由最终成功/失败的那次尝试统一记录。
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
	// 设计原则：
	// 1) 先按 targetPlatform 获取并解析上游响应
	// 2) 再按 inputFormat 渲染客户端响应
	// 这样输入协议与下游平台彻底解耦，避免协议错配。
	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, false)
		if err != nil {
			log.Printf("Error forwarding Claude request: %v", err)
			result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil, "")
			return result
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody, "application/json")
			return result
		}

		var claudeResp relay.ClaudeResponse
		respBody, err := readBodyAndJSON(httpResp, &claudeResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			log.Printf("Error parsing Claude response: %v", err)
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to parse response: %v", err), nil, "")
			return result
		}

		applyProviderUsageToRecord(record, extractProviderUsageFromBody(targetPlatform, "", respBody))
		applyLocalResponseEstimate(record, extractOutputTextFromProviderBody(targetPlatform, "", respBody), s.config.GetUsageConfig())
		actualTokens := getInt(record.Usage.TotalTokens)
		s.adjustTokenUsage(group.ID, actualTokens)

		canonicalResp, canonicalErr := relay.ClaudeResponseToCanonical(&claudeResp)
		if canonicalErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to convert Claude response to Maheshvara: %v", canonicalErr), nil, "")
			return result
		}
		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		record.StatusCode = http.StatusOK
		output, renderErr := renderCanonicalChatResponse(canonicalResp, inputFormat)
		if renderErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to render Maheshvara response: %v", renderErr), nil, "")
			return result
		}
		c.JSON(200, output)
		result = relayOutcome{committed: true, statusCode: 200}
		return result

	case relay.PlatformGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, false)
		if err != nil {
			log.Printf("Error forwarding Gemini request: %v", err)
			result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil, "")
			return result
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody, "application/json")
			return result
		}

		var geminiResp relay.GeminiResponse
		respBody, err := readBodyAndJSON(httpResp, &geminiResp)
		record.ProviderResponse = sanitizeUsageBody(respBody)
		if err != nil {
			log.Printf("Error parsing Gemini response: %v", err)
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to parse response: %v", err), nil, "")
			return result
		}

		canonicalResp, canonicalErr := relay.GeminiResponseToCanonical(&geminiResp)
		if canonicalErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to convert Gemini response to Maheshvara: %v", canonicalErr), nil, "")
			return result
		}
		applyProviderUsageToRecord(record, extractProviderUsageFromBody(targetPlatform, "", respBody))
		applyLocalResponseEstimate(record, extractOutputTextFromProviderBody(targetPlatform, "", respBody), s.config.GetUsageConfig())
		actualTokens := getInt(record.Usage.TotalTokens)
		s.adjustTokenUsage(group.ID, actualTokens)

		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		record.StatusCode = http.StatusOK
		output, renderErr := renderCanonicalChatResponse(canonicalResp, inputFormat)
		if renderErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to render Maheshvara response: %v", renderErr), nil, "")
			return result
		}
		c.JSON(200, output)
		result = relayOutcome{committed: true, statusCode: 200}
		return result

	default:
		resp, respBody, statusCode, err := s.openaiAdapter.SendRequestRawWithBody(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding request (status=%d): %v", statusCode, err)
			if len(respBody) > 0 {
				record.ProviderResponse = sanitizeUsageBody(respBody)
			}
			if statusCode > 0 {
				// 上游返回了真实状态码与错误体：透传给客户端（与 Claude/Gemini 分支一致），
				// 并据真实状态码决定是否故障转移。
				result = failResult(statusCode, string(respBody), respBody, "application/json")
			} else {
				// 连接层错误（无状态码）：当作可重试的 502。
				result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil, "")
			}
			return result
		}

		record.ProviderResponse = sanitizeUsageBody(respBody)
		applyProviderUsageToRecord(record, extractProviderUsageFromBody(targetPlatform, "", respBody))
		applyLocalResponseEstimate(record, extractOutputTextFromProviderBody(targetPlatform, "", respBody), s.config.GetUsageConfig())
		actualTokens := getInt(record.Usage.TotalTokens)
		s.adjustTokenUsage(group.ID, actualTokens)

		s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

		record.StatusCode = http.StatusOK
		canonicalResp, canonicalErr := relay.OpenAIChatResponseToCanonical(resp)
		if canonicalErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to convert OpenAI response to Maheshvara: %v", canonicalErr), nil, "")
			return result
		}
		output, renderErr := renderCanonicalChatResponse(canonicalResp, inputFormat)
		if renderErr != nil {
			result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to render Maheshvara response: %v", renderErr), nil, "")
			return result
		}
		c.JSON(200, output)
		result = relayOutcome{committed: true, statusCode: 200}
		return result
	}
}

func (s *Server) handleStreamRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, estimatedTokens int, record *usageRecord, isLast bool) relayOutcome {
	if relay.IsCustomPlatform(targetPlatform) {
		return s.handleCustomStreamRequest(c, group, selectedModel, customRequest, targetPlatform, inputFormat, startTime, record, isLast)
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

	// 流式失败的可重试性判定。注意：一旦开始向客户端写出 SSE 字节，
	// 就无法再重试（响应头已发出），因此重试只发生在"建立上游连接 +
	// 读到上游首个状态码"之前。
	failResult := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		retryable := shouldRetryStatus(statusCode)
		if isLast || !retryable {
			record.StatusCode = statusCode
			record.Error = errMsg
			if respBody != nil {
				c.Data(statusCode, "application/json", respBody)
			} else {
				writeStreamForwardError(c, inputFormat, fmt.Errorf("%s", errMsg))
			}
			return relayOutcome{committed: true, statusCode: statusCode, errMsg: errMsg}
		}
		return relayOutcome{committed: false, statusCode: statusCode, errMsg: errMsg}
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported")
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		c.JSON(500, gin.H{"error": "Streaming not supported"})
		result = relayOutcome{committed: true, statusCode: 500, errMsg: "Streaming not supported"}
		return result
	}

	// startSSE 在确认上游成功、即将写出响应体之前调用一次，写出 SSE 响应头。
	sseStarted := false
	startSSE := func() {
		if sseStarted {
			return
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		// 不手动设 Transfer-Encoding：Go 的 http.Server 对无 Content-Length 的
		// 流式响应自动 chunked，手动设是冗余且在错误路径易制造 TE+Content-Length 冲突。
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		sseStarted = true
	}

	writer := &observingStreamWriter{
		inner:        &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:       record,
		startTime:    startTime,
		observeUsage: false,
	}

	// forwardErr 收集"上游连接成功、SSE 已开始后"的流转发/转换错误。
	// 一旦 SSE 头已发出就无法改 HTTP 状态码，但必须把 record.StatusCode 从 200
	// 下调，否则中途断流/空响应会被统计与日志误判为成功。
	var forwardErr error

	switch targetPlatform {
	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
		if err != nil {
			log.Printf("Error forwarding Claude stream request: %v", err)
			result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil)
			return result
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody)
			return result
		}

		startSSE()
		record.StatusCode = http.StatusOK
		observeUpstreamUsage(httpResp, record, targetPlatform)

		forwardErr = relay.TransformStreamViaMaheshvara(c.Request.Context(), httpResp, relay.FormatClaude, inputFormat, writer, selectedModel.Name)

	case relay.PlatformGemini:
		httpResp, err := s.geminiAdapter.SendRequest(selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, true)
		if err != nil {
			log.Printf("Error forwarding Gemini stream request: %v", err)
			result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil)
			return result
		}
		if httpResp.StatusCode != http.StatusOK {
			defer httpResp.Body.Close()
			respBody, _ := io.ReadAll(httpResp.Body)
			result = failResult(httpResp.StatusCode, string(respBody), respBody)
			return result
		}

		startSSE()
		record.StatusCode = http.StatusOK
		observeUpstreamUsage(httpResp, record, targetPlatform)

		forwardErr = relay.TransformStreamViaMaheshvara(c.Request.Context(), httpResp, relay.FormatGemini, inputFormat, writer, selectedModel.Name)

	default:
		resp, err := s.openaiAdapter.SendRequestStream(selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding stream request: %v", err)
			result = failResult(http.StatusBadGateway, fmt.Sprintf("Failed to forward request: %v", err), nil)
			return result
		}

		startSSE()
		record.StatusCode = http.StatusOK
		observeUpstreamUsage(resp, record, targetPlatform)

		if record.RelayMode == "passthrough" {
			// OpenAI 系同协议透传：原始转发上游 SSE，保留 tool call id、
			// reasoning_content 等字段，不经过 Maheshvara 重渲染。
			forwardErr = relay.ForwardOpenAIStream(c.Request.Context(), resp, writer)
		} else {
			forwardErr = relay.TransformStreamViaMaheshvara(c.Request.Context(), resp, relay.FormatOpenAIChat, inputFormat, writer, selectedModel.Name)
		}
	}

	// 上游已建连、SSE 已开始后的转发/转换错误：HTTP 状态码已无法更改，
	// 但必须把 record.StatusCode 从 200 下调为 502 并记录错误，否则中途断流/
	// 空响应会在 usage 日志与统计里被误判为成功。
	if forwardErr != nil {
		log.Printf("Error forwarding stream after SSE started: %v", forwardErr)
		record.Error = forwardErr.Error()
		if record.StatusCode < 400 {
			record.StatusCode = http.StatusBadGateway
		}
	} else if streamYieldedNothing(record, writer) {
		// 上游返回 200 但既无任何输出文本、也无 usage —— 实际是空响应。
		// 这类"看似成功实则空结构体"必须记为失败，否则日志/统计误判为成功。
		log.Printf("Upstream stream returned empty response (no content, no usage)")
		record.Error = "upstream returned empty response"
		record.StatusCode = http.StatusBadGateway
	}

	applyLocalResponseEstimate(record, writer.responseText.String(), s.config.GetUsageConfig())
	s.logDebug("Stream request completed in %dms", time.Since(startTime).Milliseconds())
	result = relayOutcome{committed: true, statusCode: record.StatusCode}
	return result
}

// streamYieldedNothing 判断一次"无错误"的流式转发是否实际为空响应：
// 既没有任何输出文本，也没有捕获到任何 usage token。用于把上游 200 空响应
// 从"成功"纠正为失败。
func streamYieldedNothing(record *usageRecord, writer *observingStreamWriter) bool {
	if writer.responseText.Len() > 0 {
		return false
	}
	if getInt(record.Usage.TotalTokens) > 0 || getInt(record.Usage.OutputTokens) > 0 {
		return false
	}
	return true
}

func readBodyAndJSON(resp *http.Response, v interface{}) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, json.Unmarshal(body, v)
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

// selectModel 根据配置的策略选择单个模型（首选）。
// 现在复用 buildCandidates 的有序候选列表取第一个，避免旧实现里
// round-robin 用过期索引访问 models[idx] 导致的越界 panic（高危1）。
// 需要故障转移的路径应直接使用 buildCandidates 遍历全部候选。
func (s *Server) selectModel(group *config.ModelGroupConfig) config.ModelRef {
	candidates := s.buildCandidates(group)
	if len(candidates) == 0 {
		return config.ModelRef{}
	}
	return candidates[0]
}

// tokenAllowsGroup 校验当前请求的 API key 是否被允许访问指定模型组。
// 从 gin context 取 authMiddleware 写入的 AllowedGroups：为空表示不限制（放行）；
// 非空则要求 groupName 在白名单内。
func (s *Server) tokenAllowsGroup(c *gin.Context, groupName string) bool {
	value, exists := c.Get("elysiaAllowedGroups")
	if !exists {
		return true
	}
	allowed, ok := value.([]string)
	if !ok || len(allowed) == 0 {
		return true // 未设置限制 → 放行
	}
	for _, g := range allowed {
		if g == groupName {
			return true
		}
	}
	return false
}

// validateModelGroup 验证模型组配置
func (s *Server) validateModelGroup(groupName string) (*config.ModelGroupConfig, error) {
	if groupName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	group := s.findGroupByName(groupName)
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

func (s *Server) acquireRateLimit(group *config.ModelGroupConfig, estimatedTokens int) (func(), error) {
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

	// release 是单一的「结算点」：无论成功还是失败，都释放一个在途计数并
	// 退还本次预留的 estimatedTokens。实际消耗由成功路径的 adjustTokenUsage
	// 单独累加。这样**失败请求**（永不调用 adjustTokenUsage）的预留会被如数
	// 退还，不再永久占用每日 token 配额。
	released := false
	return func() {
		s.rateLimitMu.Lock()
		defer s.rateLimitMu.Unlock()
		if released {
			return // 幂等：避免重复 defer 误减
		}
		released = true

		current := s.getOrCreateRateLimitStateLocked(group.ID)
		if current.Active > 0 {
			current.Active--
		}
		if estimatedTokens > 0 {
			current.Tokens -= estimatedTokens
			if current.Tokens < 0 {
				current.Tokens = 0
			}
		}
	}, nil
}

// adjustTokenUsage 在请求成功并拿到实际 token 数后，把实际消耗累加到每日计数。
// 预留额度的退还由 acquireRateLimit 返回的 release 闭包统一负责，因此这里只加
// 实际值、不再二次扣减预留。
func (s *Server) adjustTokenUsage(groupID string, actualTokens int) {
	if actualTokens <= 0 {
		return
	}
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	state := s.getOrCreateRateLimitStateLocked(groupID)
	state.Tokens += actualTokens
	if state.Tokens < 0 {
		state.Tokens = 0
	}
}

// forgetGroupRuntimeState 删除模型组后清理其残留的限流、轮询游标与粘滞映射，
// 避免已删除组的键永远留在内存中。所有删除都在对应锁内完成。
func (s *Server) forgetGroupRuntimeState(groupID string) {
	if groupID == "" {
		return
	}
	s.rateLimitMu.Lock()
	delete(s.rateLimits, groupID)
	s.rateLimitMu.Unlock()

	s.roundRobinMutex.Lock()
	delete(s.roundRobinIndex, groupID)
	s.roundRobinMutex.Unlock()

	s.affinity.removeGroup(groupID)
}

func (s *Server) getOrCreateRateLimitStateLocked(groupID string) *rateLimitState {
	today := time.Now().Format("2006-01-02")
	state, ok := s.rateLimits[groupID]
	if !ok {
		state = &rateLimitState{Date: today}
		s.rateLimits[groupID] = state
	}
	if state.Date != today {
		// 日期翻转只重置每日配额计数（Requests/Tokens），不动 Active：
		// Active 跟踪的是「当前在途请求数」，与日期无关。跨午夜仍在途的请求
		// 其 release 会对 Active 做 --，若此处清零会导致并发计数错乱、
		// MaxConcurrency 在午夜窗口被突破。
		state.Date = today
		state.Requests = 0
		state.Tokens = 0
	}
	return state
}

func (s *Server) validateOutbound(raw string) error {
	if s.skipOutboundValidation {
		return nil
	}
	return validateOutboundBaseURL(raw)
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

// isPrivateOrRestrictedIP 委托到 relay 包的同名判定，保证「预校验」（这里，
// 解析后逐个判 IP）与「连接时校验」（relay secureControl）用同一份网段清单，
// 不再各维护一份易漂移的列表。
func isPrivateOrRestrictedIP(ip net.IP) bool {
	return relay.IsPrivateOrRestrictedIP(ip)
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

	canonicalReq, err := relay.ClaudeRequestToCanonical(bodyBytes)
	if err != nil {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to convert request: %v", err)})
		return
	}

	inputTokens := estimateCanonicalRequestUsage(canonicalReq, s.config.GetUsageConfig()).InputTokens

	c.JSON(200, gin.H{
		"input_tokens": inputTokens,
	})
}

func (s *Server) healthCheck(c *gin.Context) {
	// 公开（无鉴权）健康端点，供负载均衡 / k8s 探针使用。
	// 深入探测数据库依赖：store 不可用或 Ping 失败时返回 503，
	// 这样探针能据此摘除不健康实例。
	dbOK := false
	if s.store != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		dbOK = s.store.Ping(ctx) == nil
	}

	status := "ok"
	code := http.StatusOK
	if !dbOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, gin.H{"status": status, "database": dbOK})
}

func (s *Server) ListenAndServe() error {
	s.setupRoutes()
	s.startUsageWriter()
	s.healthChecker = newHealthChecker(s)
	s.healthChecker.start()

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	log.Printf("Starting server on %s", addr)

	// 显式持有 http.Server，便于 /__shutdown 优雅关停。
	s.httpServer = &http.Server{Addr: addr, Handler: s.engine}
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		// 被 /__shutdown 主动关停属正常退出，不视为错误。
		log.Printf("Server stopped gracefully")
		return nil
	}
	return err
}

// shutdown 处理 /__shutdown：优雅关停 http.Server（给在途请求一个超时窗口），
// 仅允许本机回环调用。供本地管理工具或用户手动优雅停止进程。
func (s *Server) shutdown(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"shuttingDown": true})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(ctx); err != nil {
				log.Printf("graceful shutdown error: %v", err)
			}
		}
		// http.Server.Shutdown 已等待在途请求结束，此时不会再有新记录入队。
		// 先停健康检查 goroutine，再冲刷 usage 队列把缓冲中的记录落库，
		// 避免优雅关停时丢失计费/统计记录与 goroutine 泄漏。
		if s.healthChecker != nil {
			s.healthChecker.shutdown()
		}
		s.stopUsageWriter()
	}()
}
