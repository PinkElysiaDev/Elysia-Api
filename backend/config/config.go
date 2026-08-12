package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	Host                   string             `json:"host,omitempty"`
	Port                   int                `json:"port,omitempty"`
	PanelAccessToken       string             `json:"panelAccessToken,omitempty"`
	DatabasePath           string             `json:"databasePath,omitempty"`
	LogLevel               string             `json:"logLevel,omitempty"`
	SecretKeyPath          string             `json:"secretKeyPath,omitempty"`
	WebUIDir               string             `json:"webuiDir,omitempty"`
	EnablePprof            bool               `json:"enablePprof,omitempty"`
	MaxBodyBytes           int64              `json:"maxBodyBytes,omitempty"`
	Server                 ServerConfig       `json:"server"`
	Tokens                 []AccessToken      `json:"-"`                                // 运行时字段：仅用于 store-nil 回退与测试；不再从 config.json 读取（模型/token 走 SQLite）
	Groups                 []ModelGroupConfig `json:"-"`                                // 同上：旧 config.json 的 modelGroups 字段已废弃，数据走 SQLite
	Responses              ResponsesConfig    `json:"responses,omitempty"`              // Responses API 兼容策略
	Relay                  RelayConfig        `json:"relay,omitempty"`                  // 转发（chat/claude/gemini）策略
	CustomProtocols        []json.RawMessage  `json:"customProtocols,omitempty"`        // Maheshvara 自定义协议 JSON 配置
	Usage                  UsageConfig        `json:"usage,omitempty"`                  // 用量估算配置
	HTTPTimeout            int                `json:"httpTimeout,omitempty"`            // HTTP 请求超时时间（秒），0 为不限制
	DebugMode              bool               `json:"debugMode,omitempty"`              // 调试模式
	VerboseLog             bool               `json:"verboseLog,omitempty"`             // 详细日志模式
	UsagePersistEnabled    *bool              `json:"usagePersistEnabled,omitempty"`    // 持久化用量统计
	UsagePersistMaxRecords int                `json:"usagePersistMaxRecords,omitempty"` // 最多保留的用量记录条数
	HealthCheck            HealthCheckConfig  `json:"healthCheck,omitempty"`            // 可选的后台健康检测
	AllowFakeIPOutbound    bool               `json:"allowFakeIPOutbound,omitempty"`    // 放行 Clash/Mihomo TUN fake-ip 段（198.18.0.0/15、240.0.0.0/4）出站，解决全局 TUN 代理下上游域名被解析为假 IP 遭 SSRF 守卫误杀
	mu                     sync.RWMutex
	path                   string
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

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type ResponsesConfig struct {
	Enabled      *bool  `json:"enabled,omitempty"`
	UpstreamMode string `json:"upstreamMode,omitempty"` // native | transform | auto

	// Deprecated: Maheshvara now uses explicit conversion errors and protocol-bound RawExtra fields.
	TransformUnsupportedBehavior string `json:"transformUnsupportedBehavior,omitempty"`
	// Deprecated: unknown fields are retained in Maheshvara RawExtra and are never blindly copied across protocols.
	PassThroughUnknownFields *bool `json:"passThroughUnknownFields,omitempty"`
}

// RelayConfig controls compatibility optimizations around the Maheshvara path.
// Same-protocol passthrough (client wire == upstream wire) is now applied
// unconditionally so provider-specific fields survive verbatim. The legacy
// Passthrough flag is retained for backward compatibility only.
type RelayConfig struct {
	// Deprecated: same-protocol passthrough is unconditional; no longer consulted.
	Passthrough *bool `json:"passthrough,omitempty"`
}

type UsageConfig struct {
	EstimateWhenMissing         *bool `json:"estimateWhenMissing,omitempty"`
	CharsPerToken               int   `json:"charsPerToken,omitempty"`
	DefaultOutputTokenEstimate  int   `json:"defaultOutputTokenEstimate,omitempty"`
	ImageInputTokenEstimate     int   `json:"imageInputTokenEstimate,omitempty"`
	FileInputTokenEstimatePerKB int   `json:"fileInputTokenEstimatePerKB,omitempty"`
}

type AccessToken struct {
	Token         string   `json:"token,omitempty"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	AllowedGroups []string `json:"allowedGroups,omitempty"` // 允许访问的模型组；空表示不限制
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
}

var GlobalConfig *Config

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.path = path
	cfg.applyBootstrapDefaults(path)

	cfg.mu.Lock()
	GlobalConfig = &cfg
	cfg.mu.Unlock()

	return &cfg, nil
}

func (c *Config) applyBootstrapDefaults(path string) {
	if c.Host == "" {
		c.Host = c.Server.Host
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = c.Server.Port
	}
	if c.Port == 0 {
		c.Port = 8765
	}
	c.Server.Host = c.Host
	c.Server.Port = c.Port
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
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.WebUIDir != "" && !filepath.IsAbs(c.WebUIDir) {
		c.WebUIDir = filepath.Join(filepath.Dir(path), c.WebUIDir)
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 32 * 1024 * 1024
	}
}

func (c *Config) Save() error {
	c.mu.RLock()
	path := c.path
	c.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	c.mu.RLock()
	raw["host"] = c.Host
	raw["port"] = c.Port
	raw["panelAccessToken"] = c.PanelAccessToken
	raw["databasePath"] = c.DatabasePath
	raw["logLevel"] = c.LogLevel
	raw["enablePprof"] = c.EnablePprof
	raw["httpTimeout"] = c.HTTPTimeout
	raw["allowFakeIPOutbound"] = c.AllowFakeIPOutbound
	if len(c.CustomProtocols) > 0 {
		protocols := make([]json.RawMessage, len(c.CustomProtocols))
		for index, protocol := range c.CustomProtocols {
			protocols[index] = append(json.RawMessage(nil), protocol...)
		}
		raw["customProtocols"] = protocols
	} else {
		delete(raw, "customProtocols")
	}
	c.mu.RUnlock()

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
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

func (c *Config) SetEnablePprof(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.EnablePprof = enabled
}

func (c *Config) GetEnablePprof() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EnablePprof
}

// IsFakeIPOutboundAllowed 返回是否放行 fake-ip 段出站。默认 false（安全）。
func (c *Config) IsFakeIPOutboundAllowed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AllowFakeIPOutbound
}

// SetAllowFakeIPOutbound 设置是否放行 fake-ip 段出站。调用方负责同步下发到
// relay 包的包级开关（见 server.syncRelaySSRFPolicy）。
func (c *Config) SetAllowFakeIPOutbound(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AllowFakeIPOutbound = v
}

func (c *Config) Reload() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}

	var newCfg Config
	if err := json.Unmarshal(data, &newCfg); err != nil {
		return err
	}

	newCfg.path = c.path
	newCfg.applyBootstrapDefaults(c.path)

	c.mu.Lock()
	c.Host = newCfg.Host
	c.Port = newCfg.Port
	c.PanelAccessToken = newCfg.PanelAccessToken
	c.DatabasePath = newCfg.DatabasePath
	c.LogLevel = newCfg.LogLevel
	c.SecretKeyPath = newCfg.SecretKeyPath
	c.WebUIDir = newCfg.WebUIDir
	c.EnablePprof = newCfg.EnablePprof
	c.MaxBodyBytes = newCfg.MaxBodyBytes
	c.Server = newCfg.Server
	// 注意：Tokens/Groups 是 json:"-" 运行时字段，不从 config.json 读取，
	// 因此热重载不覆盖它们（模型组/token 的变更走 SQLite + 路由缓存失效）。
	c.Responses = newCfg.Responses
	c.Usage = newCfg.Usage
	c.HTTPTimeout = newCfg.HTTPTimeout
	c.DebugMode = newCfg.DebugMode
	c.VerboseLog = newCfg.VerboseLog
	c.UsagePersistEnabled = newCfg.UsagePersistEnabled
	c.UsagePersistMaxRecords = newCfg.UsagePersistMaxRecords
	c.HealthCheck = newCfg.HealthCheck
	c.AllowFakeIPOutbound = newCfg.AllowFakeIPOutbound
	c.CustomProtocols = make([]json.RawMessage, len(newCfg.CustomProtocols))
	for index, protocol := range newCfg.CustomProtocols {
		c.CustomProtocols[index] = append(json.RawMessage(nil), protocol...)
	}
	c.mu.Unlock()

	return nil
}

func (c *Config) Dir() string {
	c.mu.RLock()
	path := c.path
	c.mu.RUnlock()
	return filepath.Dir(path)
}

func (c *Config) GetGroups() []ModelGroupConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// 返回副本：锁在 return 即释放，直接返回内部切片会让调用方在锁外
	// 与 Reload/admin 的并发重写竞争（go test -race 可验证）。
	return append([]ModelGroupConfig(nil), c.Groups...)
}

// GetGroupByName 根据模型组名称查找模型组配置
func (c *Config) GetGroupByName(name string) *ModelGroupConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.Groups {
		if c.Groups[i].Name == name {
			groupCopy := c.Groups[i]
			return &groupCopy
		}
	}
	return nil
}

func (c *Config) GetTokens() []AccessToken {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// 返回副本，理由同 GetGroups：避免锁外别名读取与并发重写竞争。
	return append([]AccessToken(nil), c.Tokens...)
}

func (c *Config) GetCustomProtocols() []json.RawMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	protocols := make([]json.RawMessage, len(c.CustomProtocols))
	for index, raw := range c.CustomProtocols {
		protocols[index] = append(json.RawMessage(nil), raw...)
	}
	return protocols
}

// 以下访问器/设置器统一通过 mu 锁保护那些会被请求热路径与 Reload/admin
// 并发读写的字段，避免数据竞争（go build -race 可验证）。

func (c *Config) GetServer() ServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Server
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
//  1. 环境变量 ELYSIA_API_MASTER_KEY（与配置文件加密共用同一口令）；
//  2. 数据库同目录下的 .db-key 文件，不存在则自动生成一个随机 32 字节密钥并落盘。
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

	// 向后兼容：历史版本把密钥写在 <dbDir>/.db-key。若 SecretKeyPath 尚未建立但
	// 旧密钥文件存在，沿用旧密钥——否则既有加密数据在升级后将无法解密。
	var legacyKeyPath string
	if dbPath != "" {
		legacyKeyPath = filepath.Join(filepath.Dir(dbPath), ".db-key")
		if data, err := os.ReadFile(legacyKeyPath); err == nil {
			if key := strings.TrimSpace(string(data)); key != "" {
				return []byte(key)
			}
		}
	}

	// 选定要写入的新密钥路径：优先 SecretKeyPath，回退到旧 .db-key 位置。
	target := keyPath
	if target == "" {
		target = legacyKeyPath
	}
	if target == "" {
		return nil
	}

	// 自动生成并持久化一个随机密钥（base64，约 43 字符）。
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// 修复 S4：rand 失败原本静默返回 nil（无日志）→ 明文存储且无人知晓。
		log.Printf("warning: failed to generate db encryption key: %v (secrets will be stored UNENCRYPTED)", err)
		return nil
	}
	key := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(target, []byte(key), 0o600); err != nil {
		log.Printf("warning: failed to persist db encryption key to %s: %v (secrets will be at risk if key is lost)", target, err)
		// 仍返回内存中的 key，本次进程内加密可用；但重启后无法解密，
		// 因此只在能落盘时才真正启用持久加密。
		return nil
	}
	// 修复 S1：密钥若与数据库同目录，备份/卷快照/cp -r 会同时带走密文与密钥，
	// at-rest 加密形同虚设。落到同目录时打印醒目告警，引导改用环境变量或独立路径。
	if dbPath != "" && filepath.Dir(target) == filepath.Dir(dbPath) {
		log.Printf("warning: db encryption key %s sits in the SAME directory as the database; a backup/snapshot of that directory exposes both ciphertext and key. Prefer ELYSIA_API_MASTER_KEY or point secretKeyPath at a separately-secured location.", target)
	}
	return []byte(key)
}

func (c *Config) IsPanelAccessConfigured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PanelAccessToken != ""
}

func (c *Config) IsValidAccessToken(token string) bool {
	_, ok := c.FindAccessToken(token)
	return ok
}

func (c *Config) FindAccessToken(token string) (AccessToken, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AccessToken{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, item := range c.Tokens {
		if item.Enabled && constantTimeEqual(item.Token, token) {
			return item, true
		}
	}
	return AccessToken{}, false
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

func (c *Config) IsUsagePersistEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.UsagePersistEnabled == nil || *c.UsagePersistEnabled
}

func (c *Config) GetUsagePersistMaxRecords() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.UsagePersistMaxRecords <= 0 {
		return 10000
	}
	if c.UsagePersistMaxRecords < 1000 {
		return 1000
	}
	return c.UsagePersistMaxRecords
}

func (c *Config) GetResponsesConfig() ResponsesConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cfg := c.Responses
	if cfg.Enabled == nil {
		v := true
		cfg.Enabled = &v
	}
	if strings.TrimSpace(cfg.UpstreamMode) == "" {
		cfg.UpstreamMode = "auto"
	}
	if strings.TrimSpace(cfg.TransformUnsupportedBehavior) == "" {
		cfg.TransformUnsupportedBehavior = "error"
	}
	if cfg.PassThroughUnknownFields == nil {
		v := true
		cfg.PassThroughUnknownFields = &v
	}
	return cfg
}

// GetRelayConfig returns relay policy. Same-protocol passthrough is always enabled.
func (c *Config) GetRelayConfig() RelayConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cfg := c.Relay
	if cfg.Passthrough == nil {
		v := true
		cfg.Passthrough = &v
	}
	return cfg
}

// IsRelayPassthroughEnabled 是 GetRelayConfig().Passthrough 的便捷封装。
func (c *Config) IsRelayPassthroughEnabled() bool {
	return *c.GetRelayConfig().Passthrough
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
		log.Fatalf("Failed to load config: %v", err)
	}

	if created {
		log.Printf("No config found at %s; created a default with a generated panelAccessToken.", *configFile)
		log.Printf("Generated panelAccessToken: %s", cfg.GetPanelAccessToken())
		log.Printf("Please review %s and rotate the token before exposing the service.", *configFile)
	}

	GlobalConfig = cfg
	log.Println("Config loaded successfully")
}
