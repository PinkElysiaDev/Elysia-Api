package server

import (
	"context"
	"encoding/json"
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

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/agent"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
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
	config            *config.Config
	engine            *gin.Engine
	protocolTransport *relay.ProtocolTransport
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

	// A2A 任务登记（a2a_server.go）：taskId -> 轮次条目（帧回放/快照），
	// messageId -> taskId 幂等去重。单实例内存态，容量有界。
	a2aMu         sync.Mutex
	a2aTasks      map[string]*a2aTaskEntry
	a2aMessageIDs map[string]string

	store      *storage.Store
	startupErr error

	// 异步 usage 写入：store 模式下，请求路径只把记录投递到 buffer channel，
	// 由单个 writer goroutine 落库，避免请求在 SQLite 写入（单连接串行）上阻塞。
	// usageWriter 包含队列与关闭标志（usage_writer.go），关停后入队安全降级。
	usageWriterMu sync.Mutex
	usageWriter   *usageWriterState
	// usageWriteGen 在 reset 时递增，丢掉队列里尚未落库的旧记录。
	usageWriteGen       atomic.Uint64
	usageSeq            atomic.Uint64
	usageWriteCtx       context.Context
	usageWriteCancel    context.CancelFunc
	usagePersistMu      sync.Mutex
	shutdownOnce        sync.Once
	lifecycleOnce       sync.Once
	lifecycleCtx        context.Context
	lifecycleCancel     context.CancelFunc
	shutdownRequested   chan struct{}
	shutdownRequestOnce sync.Once
	requestMu           sync.Mutex
	requestWG           sync.WaitGroup
	stopping            bool
	sourceRefreshWG     sync.WaitGroup
	// shutdownDone 在关停序列（信号或 /__shutdown 触发）完成后 close，
	// ListenAndServe 据此等待收尾后再返回，避免进程驻留。
	shutdownDone chan struct{}

	// usage 只读端点的短 TTL 响应缓存 + 并发合并（usage_cache.go）。
	usageCache usageResponseCache

	// 渠道亲和性：token+group → 上次成功模型的短 TTL 粘连映射。
	affinity *affinityCache

	// 可选的后台健康检测器（config.HealthCheck.Enabled 控制）。
	healthChecker *healthChecker

	// 后台日志清理器（usageLog.retentionDays/maxContentMB/maxRecords 控制，
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

	// 协议 Agent 引擎（agent_routes.go 惰性装配：store 就绪后首次使用时构建）。
	agentEngineOnce   sync.Once
	agentEngineMu     sync.Mutex
	agentEngineClosed bool
	agentEngineInst   *agent.Engine

	protocolServiceOnce       sync.Once
	protocolServiceInst       *protocol.Service
	protocolServiceErr        error
	isProtocolRuntimeRequired atomic.Bool
	protocolRuntimeReady      atomic.Bool
	gatewaySessions           gatewaySessionSet
	protocolUses              protocolRevisionUses
	gatewayJobs               gatewayJobService
}

func New(cfg *config.Config) *Server {
	var protocolPreparationErr error
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
		config:            cfg,
		engine:            engine,
		protocolTransport: relay.NewProtocolTransport(httpTimeout),
		roundRobinIndex:   make(map[string]int),
		rateLimits:        make(map[string]*rateLimitState),
		affinity:          newAffinityCache(),
		sourceRefreshing:  make(map[string]bool),
		sourceLastFetch:   make(map[string]sourceRefreshState),
		refreshSem:        make(chan struct{}, sourceRefreshConcurrency),
		catalog: newModelCatalog(cfg.GetModelCatalog, func() string {
			// 缓存落在数据库同目录，跟随用户的数据目录布局。
			dbPath := cfg.GetDatabasePath()
			if strings.TrimSpace(dbPath) == "" {
				return ""
			}
			return filepath.Join(filepath.Dir(dbPath), "model-catalog.json")
		}),
	}
	server.initLifecycle()
	if store, err := storage.OpenWithKey(cfg.DatabasePath, cfg.GetDBEncryptionKey()); err != nil {
		server.startupErr = fmt.Errorf("open sqlite store: %w", err)
		return server
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
		// 历史数据回填进小时级 rollup 预聚合表（后台、幂等、可断点续跑）；
		// 完成前聚合查询自动走 raw 路径，功能不受影响。
		store.StartRollupBackfill()
		protocolPreparationErr = store.PrepareProtocolUpgradeBackup(context.Background())
		if protocolPreparationErr == nil {
			if err := server.importLegacyConfig(); err != nil {
				log.Printf("failed to import legacy config into sqlite: %v", err)
			}
			// config.json 的 customProtocols 键已废弃：一次性导入 SQLite 后移除。
			server.migrateLegacyCustomProtocols()
			// 预置协议去厂商化改名（一次性、幂等；custom:<id> 平台引用同步重写）。
			server.migratePresetProtocolRenames()
			// 对账兜底：修复旧版改名迁移遗留的「行 id 列与 config 内部 id 脱节」
			//（注册键错位会让 custom:<行id> 引用解析报 not registered）。
			server.reconcileCustomProtocolConfigIDs()
			// Gemini 系模型历史 ID 前缀一次性剥离（旧拉取入库 ID 带 models/ 前缀，
			// 转发路径双前缀 404）。
			server.stripGeminiModelIDPrefixes()
			// 预置协议（四线制定义）逐条补齐/按哈希链升级；path 相对化预置升级后
			// 一次性把存量源 base 补上版本段（v1→v3 语义切换的配套迁移）。
			server.migratePresetRelativePathBases(server.seedPresetProtocols())
		}
	}
	// 存储迁移成功后才启动后台工作。
	go server.catalog.runPeriodic()
	server.syncOutboundPolicy()
	if protocolPreparationErr == nil {
		protocolPreparationErr = server.initializeProtocolRuntime(context.Background())
	} else {
		server.isProtocolRuntimeRequired.Store(true)
	}
	if protocolPreparationErr != nil {
		log.Printf("protocol migration requires repair; generation is disabled: %v", protocolPreparationErr)
	}
	if server.store != nil {
		server.logSystemEvent("info", "server started", map[string]any{
			"version": AppVersion, "host": cfg.Server.Host, "port": cfg.Server.Port,
		})
	}
	return server
}

// applyOutboundDeniedRanges 校验并整体替换出站禁止段，返回回滚函数：
// 调用方在配置落盘失败时调用回滚，恢复内存与 relay 下发，防内存/磁盘分叉。
// admin PUT 与 agent update_outbound_policy 工具共用同一条语义。
func (s *Server) applyOutboundDeniedRanges(candidates []string) (rollback func(), err error) {
	previous := append([]string(nil), s.config.GetOutboundConfig().DeniedIPRanges...)
	cleaned := make([]string, 0, len(candidates))
	for _, entry := range candidates {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	s.config.SetOutboundDeniedIPRanges(cleaned)
	s.syncOutboundPolicy()
	return func() {
		s.config.SetOutboundDeniedIPRanges(previous)
		s.syncOutboundPolicy()
	}, nil
}

// syncOutboundPolicy 把出站禁止 IP 段列表下发给 relay 包（连接时校验与预校验
// 共用）。在启动、热重载、admin/agent 改配置后调用，确保即时反映配置。
func (s *Server) syncOutboundPolicy() {
	ranges := s.config.GetOutboundConfig().DeniedIPRanges
	if ranges == nil { // 防御：未归一化的配置不允许被解释成「全放行」
		ranges = relay.DefaultDeniedIPRanges
	}
	relay.SetDeniedIPRanges(ranges)
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
			writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), &builtin.GatewayError{
				Class:   builtin.ErrorClassAuthentication,
				Message: "Incorrect API key provided",
			})
			return
		}
		if accessTokenHasScope(accessToken, storage.TokenScopeAgent) {
			// 远程访问 Key 与推理隔离：agent 作用域 Key 用于助手与 MCP 运维
			//（/mcp、/a2a、/api/agent），不得调用 /v1 推理接口。
			c.Abort()
			writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), &builtin.GatewayError{
				Class:   builtin.ErrorClassAuthentication,
				Message: "This API key is reserved for AI assistant remote control",
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

// agentRemoteAuth 是 AI 助手三个远程面（/api/agent、/mcp、/a2a）共用的
// 鉴权链：有效 API key 且带 agent 作用域。与 /v1 的 authMiddleware 分离——
// 错误体走 JSON + WWW-Authenticate，不套客户端线制渲染。
func (s *Server) agentRemoteAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		accessToken, ok := s.findAccessToken(token)
		if !ok {
			c.Header("WWW-Authenticate", `Bearer realm="elysia-agent"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing API key"})
			return
		}
		if !accessTokenHasScope(accessToken, storage.TokenScopeAgent) {
			c.Header("WWW-Authenticate", `Bearer realm="elysia-agent", scope="agent"`)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "this API key does not carry the agent scope"})
			return
		}
		c.Set("elysiaKeyName", accessToken.Name)
		c.Next()
	}
}

// agentRemoteGate 是远程面总开关：config.agentRemote.enabled=false 时整组
// 404（不暴露端点存在性）。
func (s *Server) agentRemoteGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.config.GetAgentRemote().AgentRemoteEnabled() {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "agent remote surface is disabled"})
			return
		}
		c.Next()
	}
}

func accessTokenHasScope(token config.AccessToken, scope string) bool {
	for _, item := range token.Scopes {
		if item == scope {
			return true
		}
	}
	return false
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

// logSystemEvent 落一条系统日志(运营/审计事件)。store 不可用或写失败时静默
// 降级为控制台输出——运营日志不得阻断业务路径。与模型调用日志(usage_records)
// 分表分查询面,互不混流。
func (s *Server) logSystemEvent(level, message string, fields map[string]any) {
	if s.store == nil {
		return
	}
	if err := s.store.InsertSystemLog(context.Background(), level, message, fields); err != nil {
		log.Printf("system log write failed (%s %s): %v", level, message, err)
	}
}

func (s *Server) reloadConfig(c *gin.Context) {
	oldServer := s.config.GetServer()
	oldHost := oldServer.Host
	oldPort := oldServer.Port
	oldHTTPTimeout := s.config.GetHTTPTimeout()

	if err := s.config.Reload(); err != nil {
		log.Printf("Config reload failed: %v", err)
		s.logSystemEvent("warn", "config reload failed", map[string]any{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{
			"reloaded": false,
			"error":    err.Error(),
		})
		return
	}
	s.logSystemEvent("info", "config reloaded", nil)

	newServer := s.config.GetServer()
	serverChanged := oldHost != newServer.Host || oldPort != newServer.Port
	// httpTimeout 属于 adapter 客户端参数,config.json 路径的热重载也要下发
	//(此前只有管理端 PUT 会 SetTimeout,文件路径改超时静默不生效直到重启)。
	if timeout := s.config.GetHTTPTimeout(); timeout != oldHTTPTimeout {
		log.Printf("HTTP timeout hot-reloaded: %ds -> %ds", oldHTTPTimeout, timeout)
		duration := time.Duration(timeout) * time.Second
		s.protocolTransport.SetTimeout(duration)
	}
	// 配置热更新后失效路由缓存，下次请求按新配置重建（借鉴 SyncOptions）。
	s.invalidateRouteCache()
	// 出站禁止 IP 段可能随配置变更，同步到 relay 包（即时生效）。
	// 自定义协议存 SQLite，不随 config.json 热重载：管理端点写入时即时同步。
	s.syncOutboundPolicy()
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

func (s *Server) chatCompletions(c *gin.Context) { s.serveVersionedPublicIngress(c) }
func (s *Server) responses(c *gin.Context)       { s.serveVersionedPublicIngress(c) }

// ginStreamWriter 实现 relay.StreamResponseWriter，封装 gin 的 ResponseWriter
type ginStreamWriter struct {
	writer http.ResponseWriter
}

func (w *ginStreamWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *ginStreamWriter) WriteString(data string) (int, error) {
	return io.WriteString(w.writer, data)
}

func (w *ginStreamWriter) Flush() error {
	// Gin 的 Flush 不返回错误；穿过捕获器和 Gin，读取底层 FlushError。
	writer := w.writer
	for {
		if _, ok := writer.(interface{ FlushError() error }); ok {
			break
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = unwrapper.Unwrap()
	}
	return http.NewResponseController(writer).Flush()
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
	today := usageDayKey(time.Now())
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
	return s.validateOutboundContext(context.Background(), raw)
}

func (s *Server) validateOutboundContext(ctx context.Context, raw string) error {
	if s.skipOutboundValidation {
		return nil
	}
	return validateOutboundBaseURLContext(ctx, raw)
}

func validateOutboundBaseURL(raw string) error {
	return validateOutboundBaseURLContext(context.Background(), raw)
}

func validateOutboundBaseURLContext(ctx context.Context, raw string) error {
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
	// localhost 主机名不做专门拒绝：环回段（127.0.0.0/8、::1/128）在默认禁止
	// 列表里，DNS 解析后逐 IP 判定自然覆盖；用户从列表移除环回段后 localhost
	// 随之放行，与拨号层（secureControl）语义保持一致。

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		return fmt.Errorf("dns resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("hostname resolved to no addresses")
	}

	for _, ip := range ips {
		if isDeniedIP(ip) {
			return fmt.Errorf("resolved IP %s is in the outbound deny list", ip.String())
		}
	}

	return nil
}

// isDeniedIP 委托到 relay 包的禁止列表判定，保证「预校验」（这里，
// 解析后逐个判 IP）与「连接时校验」（relay secureControl）用同一份配置，
// 不再各维护一份易漂移的列表。
func isDeniedIP(ip net.IP) bool {
	return relay.IsDeniedIP(ip)
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
	body, err := protocol.ReadBoundedBody(c.Request.Body, compiled.ResourceLimits().BufferBytes)
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
	c.JSON(code, gin.H{"status": status, "database": dbOK, "version": AppVersion, "commit": AppCommit})
}

func (s *Server) ListenAndServe() error {
	s.initLifecycle()
	defer s.doShutdown()
	if s.startupErr != nil {
		return s.startupErr
	}
	s.setupRoutes()
	s.startUsageWriter()
	if err := s.startGatewayJobs(); err != nil {
		return err
	}
	s.healthChecker = newHealthChecker(s)
	s.healthChecker.start()
	s.usageRetention = newUsageRetention(s)
	s.usageRetention.start()

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	log.Printf("Starting server on %s", addr)

	// 显式持有 http.Server，便于 /__shutdown 与信号(SIGTERM/SIGINT)优雅关停。
	s.httpServer = &http.Server{Addr: addr, Handler: http.HandlerFunc(s.serveHTTP), BaseContext: func(net.Listener) context.Context { return s.lifecycleCtx }}

	// 先绑定再 Serve：监听失败（端口占用等）时不拉起浏览器。
	listener, listenErr := net.Listen("tcp", addr)
	if listenErr != nil {
		return listenErr
	}
	launchConsoleBrowser(s.config.OpenBrowserOnStart, s.config.Server.Host, s.config.Server.Port)

	// Signals, the local shutdown endpoint and parent EOF share one shutdown sequence.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	serveDone := make(chan struct{})
	defer close(serveDone)
	go func() {
		select {
		case <-sigCh:
			log.Printf("Shutdown signal received, draining...")
		case <-s.shutdownRequested:
			log.Printf("Shutdown requested, draining...")
		case <-serveDone:
			return
		}
		s.doShutdown()
	}()

	err := s.httpServer.Serve(listener)
	if err == http.ErrServerClosed {
		// 主动关停(信号或 /__shutdown)属正常退出;等待关停序列完成。
		<-s.shutdownDone
		log.Printf("Server stopped gracefully")
		return nil
	}
	// ListenAndServe 其他错误(端口占用等):关停序列未跑,直接返回。
	return err
}

func (s *Server) initLifecycle() {
	s.lifecycleOnce.Do(func() {
		s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(context.Background())
		s.usageWriteCtx, s.usageWriteCancel = context.WithCancel(context.Background())
		s.shutdownRequested = make(chan struct{})
		s.shutdownDone = make(chan struct{})
	})
}

// RequestShutdown is safe before ListenAndServe binds its listener.
func (s *Server) RequestShutdown() {
	s.initLifecycle()
	s.shutdownRequestOnce.Do(func() { close(s.shutdownRequested) })
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.requestMu.Lock()
	if s.stopping {
		s.requestMu.Unlock()
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	s.requestWG.Add(1)
	s.requestMu.Unlock()
	defer s.requestWG.Done()
	s.engine.ServeHTTP(w, r)
}

// shutdown 处理 /__shutdown：优雅关停 http.Server（给在途请求一个超时窗口），
// 仅允许本机回环调用。供本地管理工具或用户手动优雅停止进程。
func (s *Server) shutdown(c *gin.Context) {
	s.logSystemEvent("info", "server shutdown requested", nil)
	c.JSON(http.StatusOK, gin.H{"shuttingDown": true})
	s.RequestShutdown()
}

func (s *Server) doShutdown() {
	s.initLifecycle()
	s.shutdownOnce.Do(func() {
		defer close(s.shutdownDone)
		deadline := time.Now().Add(12 * time.Second)
		stopWrites := time.AfterFunc(time.Until(deadline), s.usageWriteCancel)
		defer stopWrites.Stop()
		defer s.usageWriteCancel()
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		s.requestMu.Lock()
		s.stopping = true
		s.requestMu.Unlock()
		s.lifecycleCancel()
		if s.healthChecker != nil {
			s.healthChecker.cancel()
		}
		if s.catalog != nil {
			s.catalog.cancel()
		}
		if s.usageRetention != nil {
			s.usageRetention.cancel()
		}
		s.agentEngineMu.Lock()
		s.agentEngineClosed = true
		agentEngine := s.agentEngineInst
		s.agentEngineMu.Unlock()
		storageSafe := true
		if agentEngine != nil {
			if err := agentEngine.Shutdown(ctx); err != nil {
				log.Printf("agent shutdown error: %v", err)
				storageSafe = false
			}
		}
		// v2 网关面先于 HTTP 排空停止：WS 会话与异步任务持有长连接与后台工作。
		s.gatewaySessions.stop()
		s.gatewayJobs.stop()
		if s.httpServer != nil {
			httpCtx, stopHTTP := context.WithTimeout(ctx, 5*time.Second)
			if err := s.httpServer.Shutdown(httpCtx); err != nil {
				log.Printf("graceful shutdown error: %v", err)
				_ = s.httpServer.Close()
			}
			stopHTTP()
		}
		// Cancellation rejects new refresh tasks before waiting on both owners.
		s.sourceRefreshMu.Lock()
		s.sourceRefreshMu.Unlock()
		requestsDone := make(chan struct{})
		go func() { s.requestWG.Wait(); s.sourceRefreshWG.Wait(); close(requestsDone) }()
		select {
		case <-requestsDone:
		case <-ctx.Done():
			log.Printf("request shutdown error: %v", ctx.Err())
			storageSafe = false
		}
		if s.healthChecker != nil {
			s.healthChecker.shutdown()
		}
		if s.catalog != nil {
			s.catalog.shutdown()
		}
		if s.usageRetention != nil {
			s.usageRetention.shutdown()
		}
		s.stopUsageWriter()
		if s.store != nil && storageSafe {
			if err := s.store.Close(); err != nil {
				log.Printf("store close error: %v", err)
			}
		} else if s.store != nil {
			log.Printf("shutdown deadline reached; active work still owns storage, leaving cleanup to process exit")
		}
	})
}
