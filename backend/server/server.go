package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

	// 源级多 key round-robin 游标（方向6）：sourceID -> 当前 key 索引。
	keyRRMutex sync.Mutex
	keyRRIndex map[string]int

	// 模型拉取后台任务状态（refresh_jobs.go）：去重标志、结果快照与源间并发
	// 信号量。任务异步执行，端点发起即返回，前端轮询 refreshState。
	sourceRefreshMu  sync.Mutex
	sourceRefreshing map[string]bool
	sourceLastFetch  map[string]sourceRefreshState
	refreshSem       chan struct{}

	rateLimitMu sync.Mutex
	rateLimits  map[string]*rateLimitState

	store *storage.Store

	// 异步 usage 写入：store 模式下，请求路径只把记录投递到 buffer channel，
	// 由单个 writer goroutine 落库，避免请求在 SQLite 写入（单连接串行）上阻塞。
	// usageWriter 包含队列与关闭标志（usage_writer.go），关停后入队安全降级。
	usageWriterMu sync.Mutex
	usageWriter   *usageWriterState
	// usageWriteGen 在 reset 时递增，丢掉队列里尚未落库的旧记录。
	usageWriteGen  atomic.Uint64
	usageSeq       atomic.Uint64
	usagePersistMu sync.Mutex
	shutdownOnce   sync.Once

	// usage 只读端点的短 TTL 响应缓存 + 并发合并（usage_cache.go）。
	usageCache usageResponseCache

	// 渠道亲和性：token+group → 上次成功模型的短 TTL 粘连映射。
	affinity *affinityCache

	// 可选的后台健康检测器（config.HealthCheck.Enabled 控制）。
	healthChecker *healthChecker

	// 后台日志清理器（usageLog.retentionDays/maxStorageMB/maxRecords 控制，
	// 默认全关；孤儿资产清扫作为卫生活常开）。
	usageRetention *usageRetention

	// 资产目录体积统计的短 TTL 缓存（WalkDir 全量遍历，设置页会轮询）。
	assetsUsageMu sync.Mutex
	assetsUsage   usageAssetsUsage
	assetsUsageAt time.Time

	// httpServer 持有底层 http.Server 引用，供 /__shutdown 优雅关停使用。
	httpServer *http.Server

	// 路由缓存：把 groups+models 装配结果与 tokens 载入内存，
	// 让请求热路径无需每次查 SQLite（消除 N+1 + 单连接串行瓶颈）。
	// 借鉴 new-api 的 *_cache.go + SyncOptions：读走内存，写后失效。
	// routeCacheGeneration 是失效代际：装配期间发生失效则旧快照不得落缓存。
	routeCacheMu         sync.RWMutex
	cachedGroups         []config.ModelGroupConfig
	cachedTokens         map[string]config.AccessToken
	routeCacheLoaded     bool
	routeCacheGeneration uint64

	// 模型能力元数据目录（models.dev）：刷新模型时自动回填能力字段（方向1）。
	catalog *modelCatalog

	// 存在需要重启才能生效的配置变更（host/port/databasePath/pprof），
	// 由 adminUpdateRuntimeConfig 置位，restart-required/check 查询；进程重启自然清零。
	restartMu       sync.Mutex
	restartRequired bool

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

	// HTTP 超时（秒）；0 表示不限制（time.Duration(0) 本身即 0，无需特判）。
	httpTimeout := time.Duration(cfg.HTTPTimeout) * time.Second

	server := &Server{
		config:           cfg,
		engine:           engine,
		openaiAdapter:    relay.NewOpenAIAdapter(httpTimeout),
		claudeAdapter:    relay.NewClaudeAdapter(httpTimeout),
		geminiAdapter:    relay.NewGeminiAdapter(httpTimeout),
		roundRobinIndex:  make(map[string]int),
		rateLimits:       make(map[string]*rateLimitState),
		affinity:         newAffinityCache(),
		sourceRefreshing: make(map[string]bool),
		sourceLastFetch:  make(map[string]sourceRefreshState),
		refreshSem:       make(chan struct{}, sourceRefreshConcurrency),
		catalog: newModelCatalog(cfg.GetModelCatalog, func() string {
			// 缓存落在数据库同目录，跟随用户的数据目录布局。
			dbPath := cfg.GetDatabasePath()
			if strings.TrimSpace(dbPath) == "" {
				return ""
			}
			return filepath.Join(filepath.Dir(dbPath), "model-catalog.json")
		}),
	}
	// 目录定期后台更新（周期动态读取配置，管理页修改即时生效）。
	go server.catalog.runPeriodic()
	if store, err := storage.OpenWithKey(cfg.DatabasePath, cfg.GetDBEncryptionKey()); err != nil {
		log.Printf("failed to open sqlite store: %v", err)
	} else {
		server.store = store
		// 密钥完整性探测：master-key 丢失/更换会让全部密文行解不开——路由
		// 装配失败导致所有请求 401、管理面板 500。与其静默砖死，启动时把
		// 原因与恢复手段喊出来（恢复 .master-key 文件或设置环境变量）。
		if hasEncrypted, decryptOK, perr := store.SecretIntegrityProbe(context.Background()); perr != nil {
			log.Printf("secret integrity probe failed: %v", perr)
		} else if hasEncrypted && !decryptOK {
			log.Printf("========================================================================")
			log.Printf("FATAL-WARNING: encrypted secrets exist but the current master key cannot decrypt them.")
			log.Printf("All upstream keys / API tokens are unreadable: routing will fail with 401")
			log.Printf("and the admin panel cannot list sources/tokens until this is fixed.")
			log.Printf("Recovery: restore the original .master-key file next to the database, or set")
			log.Printf("ELYSIA_API_MASTER_KEY to the previous value. Rows are kept (secrets cleared)")
			log.Printf("so they can be re-entered from the panel once the key is restored.")
			log.Printf("========================================================================")
		}
		// 一次性资产布局迁移：旧的按请求分目录 → 扁平内容寻址 + 引用重建。
		// 幂等（无子目录即跳过）；失败只告警，交给孤儿清扫兜底。
		server.migrateUsageAssetsLayout()
		// 历史数据回填进小时级 rollup 预聚合表（后台、幂等、可断点续跑）；
		// 完成前聚合查询自动走 raw 路径，功能不受影响。
		store.StartRollupBackfill()
		if err := server.importLegacyConfig(); err != nil {
			log.Printf("failed to import legacy config into sqlite: %v", err)
		}
		// config.json 的 customProtocols 键已废弃：一次性导入 SQLite 后移除。
		server.migrateLegacyCustomProtocols()
		// 预置协议（四线制定义）在协议表为空时播种；已有用户数据不动。
		server.seedPresetProtocols()
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

func (s *Server) setupRoutes() {
	if s.config.GetMaxBodyBytes() > 0 {
		s.engine.Use(func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.config.GetMaxBodyBytes())
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
		log.Printf("WebUI mounted from embedded assets at /ui")
		return
	}

	log.Printf("WebUI is not available (no embedded assets and no valid webuiDir); /ui is disabled")
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

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		accessToken, ok := s.findAccessToken(token)
		if !ok {
			// 401 也按客户端线制渲染标准错误体(Codex/SDK 依赖 error 对象解析)。
			c.Abort()
			writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), &relay.MaheshvaraError{
				Class:   relay.ErrorClassAuthentication,
				Message: "Incorrect API key provided",
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
	oldHTTPTimeout := s.config.GetHTTPTimeout()

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
	// httpTimeout 属于 adapter 客户端参数,config.json 路径的热重载也要下发
	//(此前只有管理端 PUT 会 SetTimeout,文件路径改超时静默不生效直到重启)。
	if timeout := s.config.GetHTTPTimeout(); timeout != oldHTTPTimeout {
		log.Printf("HTTP timeout hot-reloaded: %ds -> %ds", oldHTTPTimeout, timeout)
		duration := time.Duration(timeout) * time.Second
		s.openaiAdapter.SetTimeout(duration)
		s.claudeAdapter.SetTimeout(duration)
		s.geminiAdapter.SetTimeout(duration)
	}
	// 配置热更新后失效路由缓存，下次请求按新配置重建（借鉴 SyncOptions）。
	s.invalidateRouteCache()
	// SSRF 放行策略可能随配置变更，同步到 relay 包级开关（即时生效）。
	// 自定义协议存 SQLite，不随 config.json 热重载：管理端点写入时即时同步。
	s.syncRelaySSRFPolicy()
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
	bodyBytes, ok := s.readRequestBody(c)
	if !ok {
		return
	}

	s.logVerbose("[Incoming Request Raw] %s", compactLogJSON(bodyBytes))

	// 根据请求路径判断客户端期望的输入/输出格式
	inputFormat := inputFormatFromPath(c.Request.URL.Path)
	record := s.initUsageRecord(c, startTime, bodyBytes, inputFormat)
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	s.logVerbose("[Input Format] %s", inputFormat)

	// 转换为 Maheshvara 核心请求。
	urlModel := ""
	if inputFormat == relay.FormatGemini {
		urlModel = geminiModelFromAction(c.Param("action"))
	}
	maheshvaraReq, _, maheshvaraErr := relay.ConvertRequestToMaheshvara(bodyBytes, inputFormat, urlModel)
	if maheshvaraErr != nil {
		log.Printf("Error converting request to Maheshvara: %v", maheshvaraErr)
		// 转换失败同样落 usage 记录（与 /v1/responses 路径对齐）：bodyOnErrorOnly
		// 模式下这类记录恰恰是唯一保留请求体的排查样本。
		s.failRequestError(c, record, startTime, inputFormat, &relay.MaheshvaraError{
			Class:   relay.ErrorClassInvalidRequest,
			Message: fmt.Sprintf("failed to convert request: %v", maheshvaraErr),
		})
		return
	}

	// Gemini 原生路径的模型名提取已由 ConvertRequestToMaheshvara 内部完成
	//（body 无 model 时回填 urlModel），此处无需重复推导。
	if maheshvaraJSON, err := json.Marshal(maheshvaraReq); err == nil {
		s.logVerbose("[Maheshvara Request] %s", compactLogJSON(maheshvaraJSON))
	}

	// 共用前置阶段：鉴权 → 组校验 → 候选 → 能力约束 → 预估 → 限流。
	plan, ok := s.prepareRelayPlan(c, record, startTime, maheshvaraReq, relayFailer{s: s, c: c, record: record, startTime: startTime, format: inputFormat}, true)
	if !ok {
		return
	}
	group, candidates := plan.group, plan.candidates
	filtered := plan.filtered
	defer plan.releaseLimiter()

	s.runRelayAttempts(c, record, startTime, group, candidates, inputFormat,
		func(attempt int, selectedModel config.ModelRef, isLast bool) relayAttemptStep {
			maheshvaraReq.Model = selectedModel.Name
			targetPlatform := relay.DetectPlatform(selectedModel.BaseURL, selectedModel.Platform)
			setRecordModel(record, selectedModel, targetPlatform)
			s.logDebug("Request model group: '%s' attempt %d/%d, selected: %s", group.Name, attempt+1, maxAttempts(group.MaxRetries, len(candidates)), selectedModel.Name)

			// 同源透传判定：客户端输入格式与所选上游线路 API 一致（Claude→Anthropic、
			// Gemini→Gemini、OpenAI→OpenAI 系），且本次未因 vision 过滤改写过请求体时，
			// 以原始请求字节直发上游，跳过 Maheshvara 往返——保留尚未纳入核心协议的私有字段
			// （cache_control / thinking / 各类未知扩展）。借鉴 Responses 透传与 new-api
			// 的 should_convert=false 分支。vision 过滤改写了 maheshvaraReq 而非原始字节，
			// 故 filtered=true 时必须回退到转换路径，否则被过滤的图片会随原始字节漏给上游。
			usePassthrough := !filtered && relay.FormatMatchesPlatform(inputFormat, targetPlatform)

			// 流式意图取自客户端原始请求：OpenAI/Claude 看请求体 stream 字段，
			// Gemini 看 URL action（:streamGenerateContent）。
			isStream := relay.IsStreamRequest(bodyBytes)
			if action := c.Param("action"); strings.Contains(action, ":streamGenerateContent") {
				isStream = true
				maheshvaraReq.Stream = true
			}

			var buildErr error
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
					addStreamOptions = isOpenAICompatible(targetPlatform)
				}
				targetBody, buildErr = relay.PassthroughBody(bodyBytes, passModelName, ensureStream, addStreamOptions)
				if buildErr == nil {
					record.RelayMode = RelayModePassthrough
					// OpenAI 系透传同样补齐缺失的 tool call id：部分客户端重建历史时
					// 会遗漏 tool_calls[].id，直接透传会被严格上游以 missing field id 拒绝。
					if isOpenAICompatible(targetPlatform) {
						targetBody, buildErr = relay.NormalizeOpenAIToolCallIDs(targetBody)
					}
				}
			} else if relay.IsCustomPlatform(targetPlatform) {
				customRequest, buildErr = relay.RenderRegisteredCustomProtocolRequest(maheshvaraReq, relay.CustomProtocolID(targetPlatform))
				if buildErr == nil {
					targetBody = customRequest.Body
					record.RelayMode = RelayModeTransform
				}
			} else {
				targetFormat, formatErr := relay.TargetFormatForPlatform(targetPlatform)
				if formatErr != nil {
					buildErr = formatErr
				} else {
					targetBody, buildErr = relay.MaheshvaraToTargetRequest(maheshvaraReq, targetFormat, nil)
				}
				if buildErr == nil {
					record.RelayMode = RelayModeTransform
				}
			}
			if buildErr != nil {
				skip := fmt.Errorf("Failed to build upstream request: %w", buildErr)
				return relayAttemptStep{
					skipErr:    skip,
					skipStatus: http.StatusBadRequest,
					skipClass:  relay.ErrorClassInvalidRequest,
				}
			}
			record.OutgoingBody = record.sanitizeBody(targetBody)
			s.logVerbose("[Outgoing Request] passthrough=%v baseUrl=%s body=%s", usePassthrough, selectedModel.BaseURL, compactLogJSON(targetBody))

			// 非透传路径仍需为流式补齐 stream 标记（透传已在 PassthroughBody 内处理）。
			if isStream && !usePassthrough && !relay.IsCustomPlatform(targetPlatform) {
				var streamBodyErr error
				targetBody, streamBodyErr = ensureStreamFlagInTargetBody(targetBody, targetPlatform)
				if streamBodyErr != nil {
					skip := fmt.Errorf("Failed to prepare stream request: %w", streamBodyErr)
					return relayAttemptStep{
						skipErr:    skip,
						skipStatus: http.StatusInternalServerError,
						skipClass:  relay.ErrorClassServer,
					}
				}
				record.OutgoingBody = record.sanitizeBody(targetBody)
			}

			if isStream {
				record.Stream = true
				return relayAttemptStep{outcome: s.handleStreamRequest(c, group, selectedModel, targetBody, customRequest, targetPlatform, inputFormat, startTime, record, isLast)}
			}
			return relayAttemptStep{outcome: s.handleNormalRequest(c, group, selectedModel, targetBody, customRequest, targetPlatform, inputFormat, startTime, record, isLast)}
		})
}

func (s *Server) handleNormalRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
	if relay.IsCustomPlatform(targetPlatform) {
		return s.handleCustomNormalRequest(c, group, selectedModel, customRequest, targetPlatform, inputFormat, startTime, record, isLast)
	}
	// failResult 在转发失败时决定是提交错误响应（最后一次尝试或不可重试），
	// 还是返回 committed=false 让上层故障转移到下一个候选模型。
	failResult := func(statusCode int, errMsg string, respBody []byte, contentType string) relayOutcome {
		return relayFailOutcome(record, isLast, shouldRetryStatus(statusCode), statusCode, errMsg, func() {
			if respBody != nil {
				writeUpstreamError(c, inputFormat, targetPlatform, statusCode, respBody, contentType)
				return
			}
			writeProtocolError(c, inputFormat, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: statusCode, Message: errMsg})
		})
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
	// 统一取回:四类上游(responses/anthropic/gemini/openai 系)的
	// 「发送→判错→非 2xx 读体→转 Maheshvara」骨架收敛于 fetchAsMaheshvara。
	targetFormat := relay.FormatOpenAIChat
	if f, ferr := relay.TargetFormatForPlatform(targetPlatform); ferr == nil {
		targetFormat = f
	}
	fetched, err := s.fetchAsMaheshvara(c.Request.Context(), selectedModel, targetFormat, targetBody)
	if fetched.respBody != nil {
		record.ProviderResponse = record.sanitizeBody(fetched.respBody)
	}
	if err != nil {
		status := fetched.status
		if status <= 0 {
			status = http.StatusBadGateway
		}
		result = failResult(status, err.Error(), fetched.respBody, contentTypeJSON)
		return result
	}
	switch targetFormat {
	case relay.FormatResponses:
		record.ConversionChain = append(record.ConversionChain, "openai_responses_response")
	case relay.FormatClaude:
		record.ConversionChain = append(record.ConversionChain, "anthropic_response")
	case relay.FormatGemini:
		record.ConversionChain = append(record.ConversionChain, "gemini_response")
	default:
		record.ConversionChain = append(record.ConversionChain, "openai_chat_response")
	}
	s.settleMaheshvaraUsage(group, record, startTime, fetched.maheshvara)
	s.logDebug("Request completed in %dms", time.Since(startTime).Milliseconds())

	// 上游 200 但响应体是错误对象:按线制输出标准错误体(带真实分类/码)。
	if fetched.maheshvara.Error != nil {
		mErr := fetched.maheshvara.Error
		mErr.Class = mErr.Class.OrDefault()
		result = failResult(mErr.EffectiveStatus(), mErr.Message, nil, "")
		return result
	}
	record.StatusCode = http.StatusOK
	output, renderErr := renderMaheshvaraChatResponse(fetched.maheshvara, inputFormat)
	if renderErr != nil {
		result = failResult(http.StatusInternalServerError, fmt.Sprintf("Failed to render Maheshvara response: %v", renderErr), nil, "")
		return result
	}
	c.JSON(200, output)
	result = relayOutcome{committed: true, statusCode: 200}
	return result
}

func (s *Server) handleStreamRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, targetBody []byte, customRequest *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
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

	// upstreamErrorStatus 从错误中提取上游真实状态码（UpstreamStatusError），
	// 无则回退 fallback——永久错误（401/403/400）不得洗白成可重试的 502。
	// 流式失败的可重试性判定。注意：一旦开始向客户端写出 SSE 字节，
	// 就无法再重试（响应头已发出），因此重试只发生在"建立上游连接 +
	// 读到上游首个状态码"之前。
	failResult := func(statusCode int, errMsg string, respBody []byte) relayOutcome {
		return relayFailOutcome(record, isLast, shouldRetryStatus(statusCode), statusCode, errMsg, func() {
			if respBody != nil {
				// 透传真实上游状态码（failResult 的 statusCode 已经过
				// upstreamErrorStatus 提取）：固定 502 会让客户端看到
				// 502 而日志记的是 401/403 等永久错误。
				writeUpstreamError(c, inputFormat, targetPlatform, statusCode, respBody, contentTypeJSON)
				return
			}
			writeProtocolError(c, inputFormat, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: statusCode, Message: errMsg})
		})
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("Streaming not supported")
		writeProtocolError(c, inputFormat, &relay.MaheshvaraError{Class: relay.ErrorClassServer, Message: "streaming is not supported on this connection"})
		record.StatusCode = http.StatusInternalServerError
		record.Error = "Streaming not supported"
		result = relayOutcome{committed: true, statusCode: 500, errMsg: "Streaming not supported"}
		return result
	}

	// startSSE 在确认上游成功、即将写出响应体之前调用一次，写出 SSE 响应头。
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

	// forwardErr 收集"上游连接成功、SSE 已开始后"的流转发/转换错误。
	// 一旦 SSE 头已发出就无法改 HTTP 状态码，但必须把 record.StatusCode 从 200
	// 下调，否则中途断流/空响应会被统计与日志误判为成功。
	var forwardErr error

	switch targetPlatform {
	case relay.PlatformResponses:
		resp, err := s.openaiAdapter.SendResponsesStream(c.Request.Context(), selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding Responses stream request: %v", err)
			result = failResult(upstreamErrorStatus(err, http.StatusBadGateway), fmt.Sprintf("Failed to forward request: %v", err), upstreamErrorBody(err))
			return result
		}

		startSSE()
		record.StatusCode = http.StatusOK
		observeUpstreamUsage(resp, record, targetPlatform)

		forwardErr = relay.TransformStreamViaMaheshvara(c.Request.Context(), resp, relay.FormatResponses, inputFormat, writer, selectedModel.Name)

	case relay.PlatformAnthropic:
		httpResp, err := s.claudeAdapter.SendRequest(c.Request.Context(), selectedModel.BaseURL, selectedModel.APIKey, targetBody, true)
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
		httpResp, err := s.geminiAdapter.SendRequest(c.Request.Context(), selectedModel.BaseURL, selectedModel.APIKey, selectedModel.Name, targetBody, true)
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
		resp, err := s.openaiAdapter.SendRequestStream(c.Request.Context(), selectedModel.BaseURL, selectedModel.APIKey, targetBody)
		if err != nil {
			log.Printf("Error forwarding stream request: %v", err)
			// 上游真实状态码保真：401/403/400 等永久错误不得洗白成 502
			// 触发对全部候选的扇出重试;错误体经 UpstreamStatusError 携带,
			// 交给 failResult 按信封解析渲染。
			result = failResult(upstreamErrorStatus(err, http.StatusBadGateway), fmt.Sprintf("Failed to forward request: %v", err), upstreamErrorBody(err))
			return result
		}

		startSSE()
		record.StatusCode = http.StatusOK
		observeUpstreamUsage(resp, record, targetPlatform)

		if record.RelayMode == RelayModePassthrough {
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

	s.settleStreamUsage(group, record, startTime)
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
	if derefInt(record.Usage.TotalTokens) > 0 || derefInt(record.Usage.OutputTokens) > 0 {
		return false
	}
	return true
}

func readBodyAndJSON(resp *http.Response, v interface{}) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, relay.MaxUpstreamBodyBytes))
	if err != nil {
		return nil, err
	}
	return body, json.Unmarshal(body, v)
}

// writeUpstreamError 写上游失败:与客户端共用同一错误信封时原样透传
// (保真),否则把上游错误体解析为核心错误后按客户端线制重渲染(自定义
// 协议平台按 OpenAI 形态尽力解析,失败回退原文摘要)。
func writeUpstreamError(c *gin.Context, inputFormat relay.FormatType, targetPlatform relay.Platform, statusCode int, respBody []byte, contentType string) {
	upstreamFormat := relay.FormatOpenAI
	if f, err := relay.TargetFormatForPlatform(targetPlatform); err == nil {
		upstreamFormat = f
	}
	if relay.SameErrorEnvelope(upstreamFormat, inputFormat) {
		c.Data(statusCode, contentType, respBody)
		return
	}
	writeProtocolError(c, inputFormat, relay.ParseUpstreamError(upstreamFormat, statusCode, respBody))
}

// upstreamErrorBody 从错误链中提取上游错误体(adapter 非 200 时返回的
// UpstreamStatusError 自带响应体);没有则返回 nil。
func upstreamErrorBody(err error) []byte {
	var statusErr *relay.UpstreamStatusError
	if errors.As(err, &statusErr) && statusErr.Body != "" {
		return []byte(statusErr.Body)
	}
	return nil
}

// ensureStreamFlagInTargetBody 在需要流式转发时，为上游请求补齐 stream=true。
// 注意：Gemini 原生接口通过 URL action 决定是否流式，不应注入 stream 字段。
func ensureStreamFlagInTargetBody(
	targetBody []byte,
	targetPlatform relay.Platform,
) ([]byte, error) {
	if targetPlatform == relay.PlatformGemini {
		// Gemini 原生接口经 URL action 决定流式,不注入 stream 字段。
		return targetBody, nil
	}
	// 注入逻辑与透传路径同源(PassthroughBody):stream=true + OpenAI 系
	// 补 stream_options.include_usage 帮助下游返回 usage chunk。
	return relay.PassthroughBody(targetBody, "", true, isOpenAICompatible(targetPlatform))
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

// validateModelGroup 验证模型组配置,失败返回按稳定错误分类组织的核心错误
// (各线制的状态码/type/code 由分类派生;消息不暴露内部「组」概念)。
func (s *Server) validateModelGroup(groupName string) (*config.ModelGroupConfig, *relay.MaheshvaraError) {
	if groupName == "" {
		return nil, &relay.MaheshvaraError{Class: relay.ErrorClassInvalidRequest, Message: "model name is required"}
	}
	group := s.findGroupByName(groupName)
	if group == nil || len(group.Models) == 0 {
		// 组不存在与组内无可用模型对客户端同义:该模型不可用。
		// 4xx 让 SDK/Codex 停止自动重试并正确提示。
		return nil, &relay.MaheshvaraError{
			Class:   relay.ErrorClassModelNotFound,
			Message: fmt.Sprintf("The model '%s' does not exist or is not available", groupName),
		}
	}
	if !group.Enabled {
		return nil, &relay.MaheshvaraError{
			Class:   relay.ErrorClassPermission,
			Message: fmt.Sprintf("The model '%s' is disabled by the administrator", groupName),
		}
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
	// 捕获 acquire 当日日期:午夜翻转后 state 的 Requests/Tokens 已被清零,
	// 在途请求的结算若仍作用于新一天,会把新一天的预留/计数一并抹掉
	// (Tokens 减成负数被钳 0)或把旧一天消耗计入新一天。
	acquiredDate := state.Date

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
		if estimatedTokens > 0 && current.Date == acquiredDate {
			// 跨日:旧一天的预留直接丢弃,不减新一天的计数。
			current.Tokens -= estimatedTokens
			if current.Tokens < 0 {
				current.Tokens = 0
			}
		}
	}, nil
}

// adjustTokenUsage 在请求成功并拿到实际 token 数后，把实际消耗累加到每日计数。
// 预留额度的退还由 acquireRateLimit 返回的 release 闭包统一负责，因此这里只加
// 实际值、不再二次扣减预留。settledOn 为请求开始（acquire）所在日期:跨日的
// 在途请求其实际消耗计入旧一天=直接丢弃,不污染新一天的计数。
func (s *Server) adjustTokenUsage(groupID string, actualTokens int, settledOn string) {
	if actualTokens <= 0 {
		return
	}
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	state := s.getOrCreateRateLimitStateLocked(groupID)
	if settledOn != "" && state.Date != settledOn {
		return // 已跨日:丢弃(计入旧一天等价于不写)。
	}
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
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeProtocolError(c, relay.FormatClaude, &relay.MaheshvaraError{
			Class: relay.ErrorClassInvalidRequest, Message: fmt.Sprintf("failed to read request body: %v", err),
		})
		return
	}

	maheshvaraReq, err := relay.AnthropicToMaheshvara(bodyBytes)
	if err != nil {
		writeProtocolError(c, relay.FormatClaude, &relay.MaheshvaraError{
			Class: relay.ErrorClassInvalidRequest, Message: fmt.Sprintf("failed to convert request: %v", err),
		})
		return
	}

	inputTokens := estimateMaheshvaraRequestUsage(maheshvaraReq, s.config.GetUsageConfig()).InputTokens

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
	s.usageRetention = newUsageRetention(s)
	s.usageRetention.start()

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	log.Printf("Starting server on %s", addr)

	// 显式持有 http.Server，便于 /__shutdown 与信号(SIGTERM/SIGINT)优雅关停。
	s.httpServer = &http.Server{Addr: addr, Handler: s.engine}

	// 信号到达时在与 /__shutdown 相同的关停序列上收尾:冲刷 usage 队列、
	// 停后台任务,再退出主 goroutine(ListenAndServe 已在关停序列内被 Close)。
	sigErr := make(chan error, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		log.Printf("Shutdown signal received, draining...")
		s.doShutdown()
		sigErr <- nil
	}()

	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		// 主动关停(信号或 /__shutdown)属正常退出;等待关停序列完成。
		<-sigErr
		log.Printf("Server stopped gracefully")
		return nil
	}
	// ListenAndServe 其他错误(端口占用等):关停序列未跑,直接返回。
	select {
	case <-sigErr:
	default:
	}
	return err
}

// shutdown 处理 /__shutdown：优雅关停 http.Server（给在途请求一个超时窗口），
// 仅允许本机回环调用。供本地管理工具或用户手动优雅停止进程。
func (s *Server) shutdown(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"shuttingDown": true})
	go s.shutdownOnce.Do(s.doShutdown)
}

func (s *Server) doShutdown() {
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
	// 目录周期循环同样停机（裸 for+sleep 会泄漏 goroutine）。
	if s.catalog != nil {
		s.catalog.shutdown()
	}
	// 日志清理可能正在删行/删资产目录，先等它结束再冲刷 usage 队列。
	if s.usageRetention != nil {
		s.usageRetention.shutdown()
	}
	s.stopUsageWriter()
}
