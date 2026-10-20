package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/atomicfile"
	log "github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

type Config struct {
	Host             string              `json:"host,omitempty"`
	Port             int                 `json:"port,omitempty"`
	PanelAccessToken string              `json:"panelAccessToken,omitempty"`
	DatabasePath     string              `json:"databasePath,omitempty"`
	LogDatabasePath  string              `json:"logDatabasePath,omitempty"`
	LogLevel         string              `json:"logLevel,omitempty"`
	LogFormat        string              `json:"logFormat,omitempty"`
	LogPersistLevel  string              `json:"logPersistLevel,omitempty"`
	SecretKeyPath    string              `json:"secretKeyPath,omitempty"`
	WebUIDir         string              `json:"webuiDir,omitempty"`
	EnablePprof      bool                `json:"enablePprof,omitempty"`
	MaxBodyBytes     int64               `json:"maxBodyBytes,omitempty"`
	Responses        ResponsesConfig     `json:"responses,omitempty"` // Responses API 开关
	Usage            UsageConfig         `json:"usage,omitempty"`     // 用量估算配置
	SystemLog        LogRetentionConfig  `json:"systemLog,omitempty"`
	UsageLog         UsageLogConfig      `json:"usageLog,omitempty"`     // 请求日志留存与内容策略
	HTTPTimeout      int                 `json:"httpTimeout,omitempty"`  // HTTP 请求超时时间（秒），0 为不限制
	DebugMode        bool                `json:"debugMode,omitempty"`    // 调试模式
	VerboseLog       bool                `json:"verboseLog,omitempty"`   // 详细日志模式
	HealthCheck      HealthCheckConfig   `json:"healthCheck,omitempty"`  // 可选的后台健康检测
	Outbound         OutboundConfig      `json:"outbound,omitempty"`     // 出站网络策略：禁止拨号的 IP 段（CIDR 列表，可编辑）
	ModelCatalog     ModelCatalogConfig  `json:"modelCatalog,omitempty"` // 模型能力元数据目录（默认 models.dev）
	AgentRemote      AgentRemoteConfig   `json:"agentRemote,omitempty"`  // AI 助手远程暴露面（REST/MCP/A2A）
	Agent            AgentDefaultsConfig `json:"agent,omitempty"`        // AI 助手全局默认（工具循环上限等）
	// OpenBrowserOnStart 控制启动时是否在系统默认浏览器打开控制台。
	// nil = 默认尝试（桌面开箱即用；无桌面环境命令缺失时静默跳过），
	// false = 不打开（子进程托管场景），true = 强制尝试。
	OpenBrowserOnStart *bool `json:"openBrowserOnStart,omitempty"`
	mu                 sync.RWMutex
	path               string
}

// ModelCatalogConfig 控制模型能力元数据目录：模型刷新时按模型 id 匹配目录条目，
// 自动回填 vision/tools/structured/thinking/maxTokens 等能力字段（未命中保持手动值）。
// 默认数据源为 https://models.dev/api.json；网络受限环境可配置镜像 URL 或出站代理。
type ModelCatalogConfig struct {
	Enabled *bool  `json:"enabled,omitempty"` // 默认启用；false 时完全停用目录（纯手动）
	URL     string `json:"url,omitempty"`     // 目录 JSON 地址，空则用默认 models.dev
	Proxy   string `json:"proxy,omitempty"`   // 拉取目录使用的出站代理（如 http://127.0.0.1:7890），空则走全局出站代理/直连
	// SyncIntervalMinutes 为目录定期刷新周期（分钟）。nil/未配置 = 默认 1440；
	// **显式 0 = 不启用定期后台同步**（仅使用内置快照与本地缓存，管理页
	// 「立即更新」仍可用）。指针类型用于区分「未配置」与「显式 0」。
	SyncIntervalMinutes *int `json:"syncIntervalMinutes,omitempty"`
}

// AgentRemoteConfig 控制 AI 助手的三个远程暴露面（/api/agent/*、/mcp、/a2a）。
// 面本身始终要求 Bearer API key 且带 agent 作用域；Enabled=false 是整组
// 下线的总开关（默认启用）。
type AgentRemoteConfig struct {
	Enabled   *bool  `json:"enabled,omitempty"`   // 默认启用；false 时三个远程面全部 404
	PublicURL string `json:"publicUrl,omitempty"` // 对外基础地址（如 https://gw.example.com），Agent Card 绝对 URL 用；空则按请求 Host 推导
}

// AgentRemoteEnabled 报告远程面是否启用（默认 true）。
func (c AgentRemoteConfig) AgentRemoteEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// ModelCatalogSyncInterval 返回生效的刷新周期与是否启用定期同步：
// nil → 默认 1440 分钟（启用）；0 → 不启用（仅快照/缓存）；>0 → 该值（启用）。
func (c ModelCatalogConfig) ModelCatalogSyncInterval() (time.Duration, bool) {
	if c.SyncIntervalMinutes == nil {
		return 24 * time.Hour, true
	}
	if *c.SyncIntervalMinutes <= 0 {
		return 0, false
	}
	return time.Duration(*c.SyncIntervalMinutes) * time.Minute, true
}

// HealthCheckConfig 控制可选的后台模型健康检测。默认关闭（Enabled=false）。
// 启用后，后台 goroutine 周期性探测各模型，连续失败则自动禁用（available=0），
// 探测恢复后自动重新启用。
type HealthCheckConfig struct {
	Enabled          bool `json:"enabled,omitempty"`
	IntervalSeconds  int  `json:"intervalSeconds,omitempty"`  // 探测间隔，默认 300s
	TimeoutSeconds   int  `json:"timeoutSeconds,omitempty"`   // 单次探测超时，默认 10s
	FailureThreshold int  `json:"failureThreshold,omitempty"` // 连续失败多少次后禁用，默认 3
}

// OutboundConfig 是出站网络策略：禁止拨号的 IP 段（CIDR）列表。
// 默认 = relay.DefaultDeniedIPRanges（私网/环回/保留段全禁，即 SSRF 防护）；
// 列表可整体替换（运行时配置页 / agent 工具）：删掉环回段即可用 127.0.0.1
// 本机上游，清空 = 全放行。nil（未配置）与空列表语义不同：前者取默认预置，
// 后者是显式的「不禁止任何段」，落盘为 "deniedIpRanges": []。
type OutboundConfig struct {
	ProxyURL       string   `json:"proxyUrl,omitempty"`
	DeniedIPRanges []string `json:"deniedIpRanges,omitempty"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type ResponsesConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type UsageConfig struct {
	EstimateWhenMissing         *bool `json:"estimateWhenMissing,omitempty"`
	CharsPerToken               int   `json:"charsPerToken,omitempty"`
	DefaultOutputTokenEstimate  int   `json:"defaultOutputTokenEstimate,omitempty"`
	ImageInputTokenEstimate     int   `json:"imageInputTokenEstimate,omitempty"`
	FileInputTokenEstimatePerKB int   `json:"fileInputTokenEstimatePerKB,omitempty"`
}

// UsageLogConfig 控制请求日志（usage_records）的留存与内容策略。
// 未配置时保留 14 天、最多 1024 MiB 内容；显式 0 关闭对应限制。
//
// 字段全部用指针以区分「未配置（走默认）」与「显式 0（关闭/不保存）」，
// 管理端 PUT runtime-config 据此实现局部更新。
type UsageLogConfig struct {
	// PersistEnabled 是日志持久化总开关，默认 true；false 时完全不落库。
	PersistEnabled *bool `json:"persistEnabled,omitempty"`
	// RetentionDays>0 时自动清理 started_at 早于该天数的记录。
	RetentionDays *int `json:"retentionDays,omitempty"`
	// MaxContentMB>0 时按日志 JSON 与去重媒体的内容字节数限额，
	// 超限按最旧优先删除记录；0=不限。
	MaxContentMB *int `json:"maxContentMB,omitempty"`
	// MaxRecords>0 时限制保留记录条数，超出删最旧；0=不限。
	MaxRecords *int `json:"maxRecords,omitempty"`
	// BodyMaxKB 是单段请求/响应正文（四段链路各一）落库上限；nil/0 默认
	// 不保存正文，仅保留元数据；正数显式开启正文保存（KB）。
	BodyMaxKB *int `json:"bodyMaxKB,omitempty"`
	// BodyOnErrorOnly 开启后仅失败请求（error 非空）保留请求体，成功请求
	// 四段 body 与外置媒体资产全部不落。默认 false。注意：它依赖 BodyMaxKB
	// 显式为正——正文捕获整体默认关闭（BodyMaxKB nil/0）时，本开关即便为
	// true 也不会让失败请求留下正文。
	BodyOnErrorOnly *bool `json:"bodyOnErrorOnly,omitempty"`
	// ExternalizeMedia 开启后请求体中的 base64 媒体（图片/音频/视频/文件）
	// 外置为独立文件，body 内以 __ELYSIA_ASSET__ 占位符替代。默认 true。
	ExternalizeMedia *bool `json:"externalizeMedia,omitempty"`
	// CleanupIntervalMinutes 是后台清理巡检周期（分钟）；nil/0=默认 60，下限 5。
	CleanupIntervalMinutes *int `json:"cleanupIntervalMinutes,omitempty"`
}

// 日志管理默认值：正文保存需显式开启。
const (
	DefaultUsageBodyMaxKB        = 0
	DefaultUsageRetentionDays    = 14
	DefaultUsageMaxContentMB     = 1024
	DefaultUsageCleanupIntervalM = 60
	MinUsageCleanupIntervalM     = 5
)

// UsageLogResolved 是 GetUsageLogConfig 归一化后的生效值（无指针语义），
// 供日志管线与清理任务直接消费。
type UsageLogResolved struct {
	PersistEnabled   bool
	RetentionDays    int
	MaxContentBytes  int64 // MaxContentMB 换算后的字节限额；0=不限
	MaxRecords       int   // 0=不限
	BodyMaxBytes     int   // 0=不保存任何请求体
	BodyOnErrorOnly  bool
	ExternalizeMedia bool
	CleanupInterval  time.Duration
}

type AccessToken struct {
	Token         string   `json:"token,omitempty"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	AllowedGroups []string `json:"allowedGroups,omitempty"` // 允许访问的模型组；空表示不限制
	Scopes        []string `json:"scopes,omitempty"`        // 端点作用域（agent=可控制 AI 助手）；空表示仅推理
}

type ModelGroupConfig struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Enabled               bool       `json:"enabled"`
	Models                []ModelRef `json:"models"`
	Strategy              string     `json:"strategy"`
	MaxRetries            int        `json:"maxRetries"`
	RetryInterval         int        `json:"retryInterval"`
	MaxConcurrency        int        `json:"maxConcurrency,omitempty"`
	DailyLimitMaxRequests int        `json:"dailyLimitMaxRequests,omitempty"`
	DailyLimitMaxTokens   int        `json:"dailyLimitMaxTokens,omitempty"`
	Type                  string     `json:"type"`
	MaxTokens             int        `json:"maxTokens,omitempty"`
	VisionCapable         *bool      `json:"visionCapable,omitempty"`
	ToolsCapable          *bool      `json:"toolsCapable,omitempty"`
}

type EndpointCapabilities struct {
	ChatCompletions       *bool `json:"chatCompletions,omitempty"`
	Responses             *bool `json:"responses,omitempty"`
	ClaudeMessages        *bool `json:"claudeMessages,omitempty"`
	GeminiGenerateContent *bool `json:"geminiGenerateContent,omitempty"`
}

type ModelRef struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	BaseURL   string                `json:"baseUrl"`
	APIKey    string                `json:"apiKey,omitempty"`
	Platform  string                `json:"platform"`
	Endpoints *EndpointCapabilities `json:"endpoints,omitempty"`
	// 模型级能力（来自模型表，目录回填/用户编辑）：用于组内候选软过滤——
	// 请求携带多模态输入或工具时优先选择声明支持的候选，不参与硬拒绝。
	VisionCapable bool `json:"visionCapable,omitempty"`
	ToolsCapable  bool `json:"toolsCapable,omitempty"`
	// 多 Key（方向6）：所属源的有效 key 列表与调度策略。非空时由
	// expandCandidatesByKeyStrategy 在请求时为每次尝试选定实际使用的 key
	// （写入克隆后的 APIKey），APIKey 保留为单 key 回退值。
	APIKeys     []string `json:"apiKeys,omitempty"`
	KeyStrategy string   `json:"keyStrategy,omitempty"`
	SourceID    string   `json:"sourceId,omitempty"`
	// CacheSynthesis 来自源级开关：目标声明 cache.breakpoints 且调用方未打满
	// 上限时，透传前补结构断点。默认关闭，不改变既有请求体。
	CacheSynthesis bool `json:"cacheSynthesis,omitempty"`
}

// Identifier 返回上行请求使用的模型标识：ID 是上游 API 的调用名（拉取模型
// 的裸 id、手动模型的「模型 ID」），Name 只是显示名（如 Gemini displayName）。
func (r ModelRef) Identifier() string {
	return r.ID
}

// GlobalConfig is published once by init for the executable entry point.
// Loading another configuration must not replace the running process's config.
var GlobalConfig *Config

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg *Config
	if err := sonic.ConfigStd.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("configuration must be a JSON object")
	}

	cfg.path = path
	cfg.applyBootstrapDefaults(path)
	cfg.applyEnvironmentOverrides()
	if _, err := relay.ParseProxyURL(cfg.Outbound.ProxyURL); err != nil {
		return nil, err
	}
	if err := protocol.ValidateRequestBodyLimit(cfg.MaxBodyBytes); err != nil {
		return nil, err
	}
	if err := cfg.validateLogSettings(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyBootstrapDefaults(path string) {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 8765
	}
	if c.DatabasePath == "" {
		c.DatabasePath = filepath.Join(filepath.Dir(path), "elysia-api.sqlite3")
	} else if !filepath.IsAbs(c.DatabasePath) {
		c.DatabasePath = filepath.Join(filepath.Dir(path), c.DatabasePath)
	}
	if c.SecretKeyPath == "" {
		c.SecretKeyPath = filepath.Join(filepath.Dir(path), ".master-key")
	} else if !filepath.IsAbs(c.SecretKeyPath) {
		c.SecretKeyPath = filepath.Join(filepath.Dir(path), c.SecretKeyPath)
	}
	if c.LogDatabasePath != "" && !filepath.IsAbs(c.LogDatabasePath) {
		c.LogDatabasePath = filepath.Join(filepath.Dir(path), c.LogDatabasePath)
		if absolute, err := filepath.Abs(c.LogDatabasePath); err == nil {
			c.LogDatabasePath = absolute
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.WebUIDir != "" && !filepath.IsAbs(c.WebUIDir) {
		c.WebUIDir = filepath.Join(filepath.Dir(path), c.WebUIDir)
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = protocol.DefaultRequestBodyBytes
	}
	c.normalizeOutboundLocked()
}

// normalizeOutboundLocked 归一化出站禁止段：未配置（nil）时物化默认预置。
// 必须持写锁（或构造期单线程）调用。
func (c *Config) normalizeOutboundLocked() {
	if c.Outbound.DeniedIPRanges != nil {
		return
	}
	c.Outbound.DeniedIPRanges = append([]string(nil), relay.DefaultDeniedIPRanges...)
}

func (c *Config) applyEnvironmentOverrides() {
	if host := strings.TrimSpace(os.Getenv("ELYSIA_API_HOST")); host != "" {
		c.Host = host
	}
}

func (c *Config) Save() error {
	// 读-改-写全程持写锁：并发 Save（多管理员同时改配置）若跨两次 RLock 段
	// 进行，会基于彼此过期的快照互相覆盖。
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

// saveLocked 是 Save 的锁内实现，供已持写锁的调用方复用，
// 保证"改内存 + 落盘"在同一个临界区内完成。
func (c *Config) saveLocked() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("configuration must be a JSON object")
	}
	fields := map[string]any{
		"host": c.Host, "port": c.Port, "panelAccessToken": c.PanelAccessToken,
		"databasePath": c.DatabasePath, "logDatabasePath": c.LogDatabasePath,
		"logLevel": c.LogLevel, "logFormat": c.LogFormat, "logPersistLevel": c.LogPersistLevel,
		"enablePprof": c.EnablePprof, "httpTimeout": c.HTTPTimeout,
	}
	for key, value := range fields {
		encoded, err := sonic.ConfigStd.Marshal(value)
		if err != nil {
			return err
		}
		raw[key] = encoded
	}
	if c.OpenBrowserOnStart != nil {
		raw["openBrowserOnStart"], _ = sonic.ConfigStd.Marshal(*c.OpenBrowserOnStart)
	} else {
		delete(raw, "openBrowserOnStart")
	}
	for key, value := range map[string]any{
		"usageLog": c.UsageLog, "systemLog": c.SystemLog, "modelCatalog": c.ModelCatalog,
		"agentRemote": c.AgentRemote, "agent": c.Agent, "outbound": c.Outbound,
	} {
		encoded, err := mergeConfigSection(raw[key], value)
		if err != nil {
			return fmt.Errorf("save configuration section %s: %w", key, err)
		}
		if string(encoded) == "{}" {
			delete(raw, key)
		} else {
			raw[key] = encoded
		}
	}
	out, err := sonic.ConfigStd.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	info, err := os.Stat(c.path)
	if err != nil {
		return err
	}
	return WriteFileAtomic(c.path, out, info.Mode().Perm())
}

// mergeConfigSection accepts the flat, JSON-tagged settings structs saved above.
// It replaces fields owned by the current section type while
// retaining unknown fields as raw JSON. Omitted known fields must also be removed
// so resetting a setting cannot resurrect its previous value on Reload.
func mergeConfigSection(original json.RawMessage, section any) ([]byte, error) {
	fields := make(map[string]json.RawMessage)
	if len(original) > 0 {
		if err := sonic.ConfigStd.Unmarshal(original, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			fields = make(map[string]json.RawMessage)
		}
	}
	sectionType := reflect.TypeOf(section)
	for index := range sectionType.NumField() {
		name, _, _ := strings.Cut(sectionType.Field(index).Tag.Get("json"), ",")
		for key := range fields {
			// The JSON codec accepts case-insensitive names for these fields.
			if strings.EqualFold(key, name) {
				delete(fields, key)
			}
		}
	}
	encoded, err := sonic.ConfigStd.Marshal(section)
	if err != nil {
		return nil, err
	}
	if err := sonic.ConfigStd.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	// An explicit empty denylist allows all addresses; omitting it restores the
	// default denylist. Keep this distinction when the struct's omitempty applies.
	if outbound, isOutbound := section.(OutboundConfig); isOutbound && outbound.DeniedIPRanges != nil {
		fields["deniedIpRanges"], _ = sonic.ConfigStd.Marshal(outbound.DeniedIPRanges)
	}
	return sonic.ConfigStd.Marshal(fields)
}

// WriteFileAtomic stages and syncs complete contents before platform-aware
// replacement. It does not promise parent-directory durability after power loss.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return atomicfile.WriteFile(path, data, perm)
}

func (c *Config) SetPanelAccessToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PanelAccessToken = token
}

func (c *Config) GetPanelAccessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PanelAccessToken
}

func (c *Config) SetDatabasePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DatabasePath = path
}

func (c *Config) GetDefaultDatabasePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return filepath.Join(filepath.Dir(c.path), "elysia-api.sqlite3")
}

// GetMaxBodyBytes 返回请求体大小上限。热路径（body-limit 中间件）每请求
// 读取，必须走锁——此前直接读字段与 Reload 的持锁写入构成数据竞争。
func (c *Config) GetMaxBodyBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.MaxBodyBytes
}

func (c *Config) SetEnablePprof(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.EnablePprof = enabled
}

// SetHost/SetPort 更新唯一的监听地址与端口。监听套接字
// 在进程启动时绑定，改动需重启才真正换监听；但配置即时落盘（Save 由调用方
// 触发），重启后即用新值——此前设置页的 host/port 只用来计算 restartRequired
// 从未应用，保存等于白保存。
func (c *Config) SetHost(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Host = host
}

func (c *Config) SetPort(port int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Port = port
}

func (c *Config) GetEnablePprof() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EnablePprof
}

// GetOutboundConfig 返回出站网络策略。DeniedIPRanges 经归一化后恒非 nil
// （未配置 = 默认预置；空列表 = 显式全放行）。
func (c *Config) GetOutboundConfig() OutboundConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Outbound
}

// SetOutboundDeniedIPRanges 整体替换禁止出站 IP 段。nil 归一化为空列表
// （显式全放行，与「未配置走默认」区分）。调用方负责校验条目为合法 CIDR、
// 同步下发到 relay 包（见 server.syncOutboundPolicy）与 Save 落盘。
func (c *Config) SetOutboundDeniedIPRanges(ranges []string) {
	if ranges == nil {
		ranges = []string{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Outbound.DeniedIPRanges = ranges
}

func (c *Config) SetOutboundProxyURL(value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Outbound.ProxyURL = strings.TrimSpace(value)
}

func (c *Config) Reload() error {
	// 全程持锁（含读文件）：此前文件读取在锁外，与「管理端 setter + Save」
	// 交错时会把刚设置并落盘的值用旧文件内容覆盖，下次 Save 即永久丢失
	// 管理员改动（丢失更新窗口）。unmarshal 开销极小，不值得为它开窗口。
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}

	var newCfg *Config
	if err := sonic.ConfigStd.Unmarshal(data, &newCfg); err != nil {
		return err
	}
	if newCfg == nil {
		return fmt.Errorf("configuration must be a JSON object")
	}

	newCfg.path = c.path
	newCfg.applyBootstrapDefaults(c.path)
	// 与 Load 同序：环境变量覆盖最后应用。否则热重载会让 ELYSIA_API_HOST
	// 部署静默失效，且随后的 Save 会把文件值固化覆盖部署配置。
	newCfg.applyEnvironmentOverrides()
	if _, err := relay.ParseProxyURL(newCfg.Outbound.ProxyURL); err != nil {
		return err
	}
	if err := protocol.ValidateRequestBodyLimit(newCfg.MaxBodyBytes); err != nil {
		return err
	}
	if err := newCfg.validateLogSettings(); err != nil {
		return err
	}

	c.publishSettingsLocked(newCfg)
	return nil
}

// GetModelCatalog 返回模型能力元数据目录配置（值类型，字段均为不可变字符串/指针，
// 无需深拷贝）。未配置时返回零值，由使用方按默认值处理。
func (c *Config) GetModelCatalog() ModelCatalogConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ModelCatalog
}

// GetAgentRemote 返回 AI 助手远程暴露面配置（含读锁快照）。
func (c *Config) GetAgentRemote() AgentRemoteConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AgentRemote
}

// defaultAgentToolLoopLimit 与 agent.DefaultMaxModelCalls 保持一致（30）；
// config 包不反向依赖 agent，此处独立声明。
const defaultAgentToolLoopLimit = 30

// AgentDefaultsConfig 汇总 AI 助手的全局默认项，运行配置页可改、热生效。
type AgentDefaultsConfig struct {
	// ToolLoopLimit 是单轮工具循环的模型调用上限；0 = 默认（30）。
	ToolLoopLimit int `json:"toolLoopLimit,omitempty"`
}

// GetAgentDefaults 返回 AI 助手全局默认配置（含读锁快照）。
func (c *Config) GetAgentDefaults() AgentDefaultsConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Agent
}

// ResolveAgentToolLoopLimit 归一化工具循环上限（<=0 回落默认 30）。
func (c *Config) ResolveAgentToolLoopLimit() int {
	if limit := c.GetAgentDefaults().ToolLoopLimit; limit > 0 {
		return limit
	}
	return defaultAgentToolLoopLimit
}

// SetAgentToolLoopLimit 运行时修改工具循环上限，随 Save() 落盘并热生效。
func (c *Config) SetAgentToolLoopLimit(limit int) {
	c.mu.Lock()
	c.Agent.ToolLoopLimit = limit
	c.mu.Unlock()
}

// ResolveModelCatalogInterval 返回供管理页表单显示的周期值（分钟）：
// nil → 默认 1440；显式 0 → 0（不启用）；>0 → 原值。
func ResolveModelCatalogInterval(c ModelCatalogConfig) int {
	if c.SyncIntervalMinutes == nil {
		return 1440
	}
	return *c.SyncIntervalMinutes
}

// SetModelCatalogSyncInterval 运行时修改目录定期同步周期（分钟）：
// >0 = 按该周期同步；0 = 不启用定期同步（仅快照/缓存）。立即生效，无需重启。
func (c *Config) SetModelCatalogSyncInterval(minutes int) {
	c.mu.Lock()
	value := minutes
	c.ModelCatalog.SyncIntervalMinutes = &value
	c.mu.Unlock()
}

// SetAgentRemote 运行时修改 AI 助手远程暴露面（REST/MCP/A2A）配置。
// 每请求读内存配置，写入即热生效（无需重启）。
func (c *Config) SetAgentRemote(remote AgentRemoteConfig) {
	c.mu.Lock()
	c.AgentRemote = remote
	c.mu.Unlock()
}

// 以下访问器/设置器统一通过 mu 锁保护那些会被请求热路径与 Reload/admin
// 并发读写的字段，避免数据竞争（go build -race 可验证）。

func (c *Config) GetServer() ServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ServerConfig{Host: c.Host, Port: c.Port}
}

func (c *Config) GetLogLevel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LogLevel
}

func (c *Config) SetLogLevel(level string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LogLevel = level
}

func (c *Config) GetHTTPTimeout() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.HTTPTimeout
}

func (c *Config) SetHTTPTimeout(seconds int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.HTTPTimeout = seconds
}

func (c *Config) IsDebugMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DebugMode
}

func (c *Config) IsVerboseLog() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DebugMode && c.VerboseLog
}

func (c *Config) GetDatabasePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DatabasePath
}

// GetHealthCheckConfig 返回应用默认值后的健康检测配置。
func (c *Config) GetHealthCheckConfig() HealthCheckConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cfg := c.HealthCheck
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 300
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 10
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	return cfg
}

// GetDBEncryptionKey 返回用于 SQLite 敏感字段（token / api_key）透明加密的密钥。
// 来源优先级：
//  1. 环境变量 ELYSIA_API_MASTER_KEY；
//  2. SecretKeyPath（默认 .master-key），不存在则生成随机密钥并落盘。
//
// 返回空切片表示无法建立密钥（理论上仅在文件系统不可写时），此时上层
// 会退化为明文存储以保证可用性，但应在日志中告警。
func (c *Config) GetDBEncryptionKey() []byte {
	if envValue := strings.TrimSpace(os.Getenv("ELYSIA_API_MASTER_KEY")); envValue != "" {
		return []byte(envValue)
	}

	c.mu.RLock()
	dbPath := c.DatabasePath
	keyPath := c.SecretKeyPath
	c.mu.RUnlock()

	// 优先使用配置的 SecretKeyPath（默认 <configDir>/.master-key）。运维显式
	// 指定的受保护路径不再被忽略（修复 S2：旧实现硬编码 .db-key、SecretKeyPath 形同摆设）。
	if keyPath != "" {
		if data, err := os.ReadFile(keyPath); err == nil {
			if key := strings.TrimSpace(string(data)); key != "" {
				return []byte(key)
			}
		}
	}

	target := keyPath
	if target == "" {
		return nil
	}

	// 自动生成并持久化一个随机密钥（base64，约 43 字符）。
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	key := base64.StdEncoding.EncodeToString(raw)
	if err := WriteFileAtomic(target, []byte(key), 0o600); err != nil {
		log.Warn().Str("component", "config").Str("path", target).Err(err).Msg("failed to persist db encryption key (secrets will be at risk if key is lost)")
		// 仍返回内存中的 key，本次进程内加密可用；但重启后无法解密，
		// 因此只在能落盘时才真正启用持久加密。
		return nil
	}
	// 修复 S1：密钥若与数据库同目录，备份/卷快照/cp -r 会同时带走密文与密钥，
	// at-rest 加密形同虚设。落到同目录时打印醒目告警，引导改用环境变量或独立路径。
	if dbPath != "" && filepath.Dir(target) == filepath.Dir(dbPath) {
		log.Warn().Str("component", "config").Str("path", target).Msg("db encryption key sits in the SAME directory as the database; a backup/snapshot exposes both ciphertext and key. Prefer ELYSIA_API_MASTER_KEY or a separately-secured secretKeyPath.")
	}
	return []byte(key)
}

func (c *Config) IsValidPanelAccessToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	c.mu.RLock()
	panelAccessToken := c.PanelAccessToken
	c.mu.RUnlock()

	return panelAccessToken != "" && constantTimeEqual(panelAccessToken, token)
}

// constantTimeEqual 以常量时间比较两个字符串，避免通过比较耗时差异
// 逐字节爆破 token（时序侧信道）。长度不同直接返回 false，但仍调用
// subtle.ConstantTimeCompare 以减少长度上的时序泄漏。
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (c *Config) GetResponsesConfig() ResponsesConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cfg := c.Responses
	if cfg.Enabled == nil {
		v := true
		cfg.Enabled = &v
	}
	return cfg
}

func (c *Config) GetUsageConfig() UsageConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cfg := c.Usage
	if cfg.EstimateWhenMissing == nil {
		v := true
		cfg.EstimateWhenMissing = &v
	}
	if cfg.CharsPerToken <= 0 {
		cfg.CharsPerToken = 4
	}
	if cfg.DefaultOutputTokenEstimate <= 0 {
		cfg.DefaultOutputTokenEstimate = 1024
	}
	if cfg.ImageInputTokenEstimate <= 0 {
		cfg.ImageInputTokenEstimate = 300
	}
	if cfg.FileInputTokenEstimatePerKB <= 0 {
		cfg.FileInputTokenEstimatePerKB = 128
	}
	return cfg
}

// DefaultUsageLogResolved 返回全默认的日志策略：持久化开启、正文保存关闭、
// 开启正文保存时默认外置媒体、自动清理关闭。供无 config 的 Server 兜底。
func DefaultUsageLogResolved() UsageLogResolved {
	return UsageLogResolved{
		PersistEnabled:   true,
		BodyMaxBytes:     DefaultUsageBodyMaxKB * 1024,
		ExternalizeMedia: true,
		CleanupInterval:  time.Duration(DefaultUsageCleanupIntervalM) * time.Minute,
	}
}

// clampIntPtr 返回钳为非负的指针副本；nil 透传。
func clampIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	n := *v
	if n < 0 {
		n = 0
	}
	return &n
}

// positiveOr 取指针正值；nil/非正返回 def（负数已在 setter 钳为 0，双保险）。
func positiveOr(p *int, def int) int {
	if p != nil && *p > 0 {
		return *p
	}
	return def
}

// GetUsageLogConfig 返回当前日志策略。
func (c *Config) GetUsageLogConfig() UsageLogResolved {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resolveUsageLogLocked()
}

// resolveUsageLogLocked 在已持读/写锁时归一化日志配置。Save 持写锁时复用。
func (c *Config) resolveUsageLogLocked() UsageLogResolved {
	cfg := c.UsageLog
	res := UsageLogResolved{
		PersistEnabled:   cfg.PersistEnabled == nil || *cfg.PersistEnabled,
		BodyOnErrorOnly:  cfg.BodyOnErrorOnly != nil && *cfg.BodyOnErrorOnly,
		ExternalizeMedia: cfg.ExternalizeMedia == nil || *cfg.ExternalizeMedia,
		CleanupInterval:  time.Duration(DefaultUsageCleanupIntervalM) * time.Minute,
	}
	res.RetentionDays = limitOrDefault(cfg.RetentionDays, DefaultUsageRetentionDays)
	if mb := limitOrDefault(cfg.MaxContentMB, DefaultUsageMaxContentMB); mb > 0 {
		res.MaxContentBytes = int64(mb) * 1024 * 1024
	}
	res.MaxRecords = positiveOr(cfg.MaxRecords, 0)
	if cfg.BodyMaxKB == nil {
		res.BodyMaxBytes = DefaultUsageBodyMaxKB * 1024
	} else if *cfg.BodyMaxKB > 0 {
		res.BodyMaxBytes = *cfg.BodyMaxKB * 1024
	}
	if cfg.CleanupIntervalMinutes != nil && *cfg.CleanupIntervalMinutes > 0 {
		minutes := *cfg.CleanupIntervalMinutes
		if minutes < MinUsageCleanupIntervalM {
			minutes = MinUsageCleanupIntervalM
		}
		res.CleanupInterval = time.Duration(minutes) * time.Minute
	}
	return res
}

// SetUsageLogConfig 运行时局部更新日志配置：仅覆盖 patch 中显式提供的字段
// （指针非 nil，数值字段 0 也是显式值），未提供的字段保持现值。
// 调用方随后调用 Save() 落盘；BodyMaxKB/开关对后续请求即时生效，
// 清理参数由后台任务在下一巡检 tick 重新读取。
func (c *Config) SetUsageLogConfig(patch UsageLogConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 局部更新：nil 字段保持现值；数值字段统一经 clampIntPtr 钳为非负
	//（归一化语义集中在 resolveUsageLogLocked，setter 只做非负化）。
	if patch.PersistEnabled != nil {
		c.UsageLog.PersistEnabled = patch.PersistEnabled
	}
	if patch.RetentionDays != nil {
		c.UsageLog.RetentionDays = clampIntPtr(patch.RetentionDays)
	}
	if patch.MaxContentMB != nil {
		c.UsageLog.MaxContentMB = clampIntPtr(patch.MaxContentMB)
	}
	if patch.MaxRecords != nil {
		c.UsageLog.MaxRecords = clampIntPtr(patch.MaxRecords)
	}
	if patch.BodyMaxKB != nil {
		c.UsageLog.BodyMaxKB = clampIntPtr(patch.BodyMaxKB)
	}
	if patch.BodyOnErrorOnly != nil {
		c.UsageLog.BodyOnErrorOnly = patch.BodyOnErrorOnly
	}
	if patch.ExternalizeMedia != nil {
		c.UsageLog.ExternalizeMedia = patch.ExternalizeMedia
	}
	if patch.CleanupIntervalMinutes != nil {
		c.UsageLog.CleanupIntervalMinutes = clampIntPtr(patch.CleanupIntervalMinutes)
	}
}

func init() {
	if strings.HasSuffix(os.Args[0], ".test") || strings.HasSuffix(os.Args[0], ".test.exe") {
		return
	}

	configFile := flag.String("config", "", "Path to config file")
	flag.Parse()

	if *configFile == "" {
		*configFile = "config.json"
	}

	cfg, created, err := EnsureConfig(*configFile)
	if err != nil {
		log.Error().Err(err).Msg("failed to load config")
		os.Exit(1)
	}

	if created {
		log.Info().Str("path", *configFile).Msg("created default config with a generated panelAccessToken")
		log.Info().Msgf("Generated panelAccessToken: %s", cfg.GetPanelAccessToken())
		log.Info().Msgf("Please review %s and rotate the token before exposing the service.", *configFile)
	}

	GlobalConfig = cfg
	log.Default().Configure(cfg.GetLoggingOptions())
	log.Info().Str("component", "config").Msg("config loaded successfully")
}

// publishSettingsLocked replaces persisted settings under the configuration lock.
func (c *Config) publishSettingsLocked(newCfg *Config) {
	c.Host = newCfg.Host
	c.Port = newCfg.Port
	c.PanelAccessToken = newCfg.PanelAccessToken
	c.DatabasePath = newCfg.DatabasePath
	c.LogDatabasePath = newCfg.LogDatabasePath
	c.LogLevel = newCfg.LogLevel
	c.LogFormat = newCfg.LogFormat
	c.LogPersistLevel = newCfg.LogPersistLevel
	c.SecretKeyPath = newCfg.SecretKeyPath
	c.WebUIDir = newCfg.WebUIDir
	c.EnablePprof = newCfg.EnablePprof
	c.MaxBodyBytes = newCfg.MaxBodyBytes
	c.Responses = newCfg.Responses
	c.Usage = newCfg.Usage
	c.UsageLog = newCfg.UsageLog
	c.SystemLog = newCfg.SystemLog
	c.ModelCatalog = newCfg.ModelCatalog
	c.HTTPTimeout = newCfg.HTTPTimeout
	c.DebugMode = newCfg.DebugMode
	c.VerboseLog = newCfg.VerboseLog
	c.HealthCheck = newCfg.HealthCheck
	c.Outbound = newCfg.Outbound
	c.AgentRemote = newCfg.AgentRemote
	c.OpenBrowserOnStart = newCfg.OpenBrowserOnStart
	c.Agent = newCfg.Agent
}
