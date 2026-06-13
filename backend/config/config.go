package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
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
	DashboardToken         string             `json:"dashboardToken,omitempty"`
	DashboardTokenEnc      *SecretValue       `json:"dashboardTokenEnc,omitempty"`
	Tokens                 []AccessToken      `json:"tokens"`
	Groups                 []ModelGroupConfig `json:"modelGroups"`
	Responses              ResponsesConfig    `json:"responses,omitempty"`              // Responses API 兼容策略
	Usage                  UsageConfig        `json:"usage,omitempty"`                  // 用量估算配置
	HTTPTimeout            int                `json:"httpTimeout,omitempty"`            // HTTP 请求超时时间（秒），0 为不限制
	DebugMode              bool               `json:"debugMode,omitempty"`              // 调试模式
	VerboseLog             bool               `json:"verboseLog,omitempty"`             // 详细日志模式
	UsagePersistEnabled    *bool              `json:"usagePersistEnabled,omitempty"`    // 持久化用量统计
	UsagePersistMaxRecords int                `json:"usagePersistMaxRecords,omitempty"` // 最多保留的用量记录条数
	HealthCheck            HealthCheckConfig  `json:"healthCheck,omitempty"`            // 可选的后台健康检测
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
	Enabled                      *bool  `json:"enabled,omitempty"`
	UpstreamMode                 string `json:"upstreamMode,omitempty"`                 // native | transform | auto
	TransformUnsupportedBehavior string `json:"transformUnsupportedBehavior,omitempty"` // error | warn | ignore
	PassThroughUnknownFields     *bool  `json:"passThroughUnknownFields,omitempty"`
}

type UsageConfig struct {
	EstimateWhenMissing         *bool `json:"estimateWhenMissing,omitempty"`
	CharsPerToken               int   `json:"charsPerToken,omitempty"`
	DefaultOutputTokenEstimate  int   `json:"defaultOutputTokenEstimate,omitempty"`
	ImageInputTokenEstimate     int   `json:"imageInputTokenEstimate,omitempty"`
	FileInputTokenEstimatePerKB int   `json:"fileInputTokenEstimatePerKB,omitempty"`
}

type SecretValue struct {
	Version    int    `json:"version,omitempty"`
	Algorithm  string `json:"algorithm,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
}

type AccessToken struct {
	Token    string       `json:"token,omitempty"`
	TokenEnc *SecretValue `json:"tokenEnc,omitempty"`
	Name     string       `json:"name"`
	Enabled  bool         `json:"enabled"`
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
	APIKeyEnc *SecretValue          `json:"apiKeyEnc,omitempty"`
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
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}

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
	if c.PanelAccessToken == "" {
		c.PanelAccessToken = c.DashboardToken
	}
	if c.DashboardToken == "" {
		c.DashboardToken = c.PanelAccessToken
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
	if err := newCfg.resolveSecrets(); err != nil {
		return err
	}

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
	c.DashboardToken = newCfg.DashboardToken
	c.DashboardTokenEnc = newCfg.DashboardTokenEnc
	c.Tokens = newCfg.Tokens
	c.Groups = newCfg.Groups
	c.Responses = newCfg.Responses
	c.Usage = newCfg.Usage
	c.HTTPTimeout = newCfg.HTTPTimeout
	c.DebugMode = newCfg.DebugMode
	c.VerboseLog = newCfg.VerboseLog
	c.UsagePersistEnabled = newCfg.UsagePersistEnabled
	c.UsagePersistMaxRecords = newCfg.UsagePersistMaxRecords
	c.HealthCheck = newCfg.HealthCheck
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
	return c.Groups
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
	return c.Tokens
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
	c.mu.RUnlock()
	if dbPath == "" {
		return nil
	}

	keyPath := filepath.Join(filepath.Dir(dbPath), ".db-key")
	if data, err := os.ReadFile(keyPath); err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			return []byte(key)
		}
	}

	// 自动生成并持久化一个随机密钥（base64，约 43 字符）。
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil
	}
	key := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		log.Printf("warning: failed to persist db encryption key to %s: %v (secrets will be at risk if key is lost)", keyPath, err)
		// 仍返回内存中的 key，本次进程内加密可用；但重启后无法解密，
		// 因此只在能落盘时才真正启用持久加密。
		return nil
	}
	return []byte(key)
}

func (c *Config) IsPanelAccessConfigured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PanelAccessToken != "" || c.DashboardToken != ""
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

func (c *Config) IsValidDashboardToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	c.mu.RLock()
	dashboardToken := c.DashboardToken
	if dashboardToken == "" {
		dashboardToken = c.PanelAccessToken
	}
	c.mu.RUnlock()

	return dashboardToken != "" && constantTimeEqual(dashboardToken, token)
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

func (c *Config) resolveSecrets() error {
	// 仅当 config.json 里确实存在加密字段（xxxEnc）时才需要 master key。
	// 新架构（WebUI + SQLite）不再向 config.json 写密文，因此裸配置可零配置启动，
	// 不再强制要求 master.key 文件存在。旧 orchestrator 写出的加密配置仍兼容。
	if !c.hasEncryptedSecrets() {
		return nil
	}

	key, err := loadMasterKey(c.path)
	if err != nil {
		return err
	}

	if c.DashboardToken == "" && c.DashboardTokenEnc != nil {
		plain, err := decryptSecret(*c.DashboardTokenEnc, key)
		if err != nil {
			return fmt.Errorf("failed to decrypt dashboard token: %w", err)
		}
		c.DashboardToken = plain
	}

	for i := range c.Tokens {
		if c.Tokens[i].Token == "" && c.Tokens[i].TokenEnc != nil {
			plain, err := decryptSecret(*c.Tokens[i].TokenEnc, key)
			if err != nil {
				return fmt.Errorf("failed to decrypt token %q: %w", c.Tokens[i].Name, err)
			}
			c.Tokens[i].Token = plain
		}
	}

	for gi := range c.Groups {
		for mi := range c.Groups[gi].Models {
			model := &c.Groups[gi].Models[mi]
			if model.APIKey == "" && model.APIKeyEnc != nil {
				plain, err := decryptSecret(*model.APIKeyEnc, key)
				if err != nil {
					return fmt.Errorf("failed to decrypt apiKey for model %q in group %q: %w", model.Name, c.Groups[gi].Name, err)
				}
				model.APIKey = plain
			}
		}
	}

	return nil
}

// hasEncryptedSecrets 报告 config.json 里是否存在任何需要 master key 解密的
// 密文字段（dashboardTokenEnc / tokenEnc / apiKeyEnc）。
// 仅在存在时才加载 master key，避免裸配置因缺少 master.key 文件而启动失败。
func (c *Config) hasEncryptedSecrets() bool {
	if c.DashboardToken == "" && c.DashboardTokenEnc != nil {
		return true
	}
	for i := range c.Tokens {
		if c.Tokens[i].Token == "" && c.Tokens[i].TokenEnc != nil {
			return true
		}
	}
	for gi := range c.Groups {
		for mi := range c.Groups[gi].Models {
			model := &c.Groups[gi].Models[mi]
			if model.APIKey == "" && model.APIKeyEnc != nil {
				return true
			}
		}
	}
	return false
}

func loadMasterKey(configPath string) ([]byte, error) {
	if envValue := strings.TrimSpace(os.Getenv("ELYSIA_API_MASTER_KEY")); envValue != "" {
		return deriveMasterKey(envValue), nil
	}

	keyPath := filepath.Join(filepath.Dir(configPath), "master.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read master key file %s: %w", keyPath, err)
	}

	keyText := strings.TrimSpace(string(data))
	if keyText == "" {
		return nil, fmt.Errorf("master key file %s is empty", keyPath)
	}

	return deriveMasterKey(keyText), nil
}

func deriveMasterKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func decryptSecret(secret SecretValue, key []byte) (string, error) {
	if secret.Algorithm != "" && secret.Algorithm != "aes-256-gcm" {
		return "", fmt.Errorf("unsupported algorithm: %s", secret.Algorithm)
	}
	if secret.Nonce == "" || secret.Ciphertext == "" {
		return "", fmt.Errorf("missing nonce or ciphertext")
	}

	nonce, err := base64.StdEncoding.DecodeString(secret.Nonce)
	if err != nil {
		return "", fmt.Errorf("invalid nonce: %w", err)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(secret.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// 必须先校验 nonce 长度：gcm.Open 在 nonce 长度不符时会 panic 而非返回 error，
	// 而 secret 来自外部 config.json（可能被篡改/损坏），不能让它崩进程。
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("invalid nonce size: got %d, want %d", len(nonce), gcm.NonceSize())
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
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

	cfg, err := Load(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	GlobalConfig = cfg
	log.Println("Config loaded successfully")
}
