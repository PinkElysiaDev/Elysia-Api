package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Server                 ServerConfig       `json:"server"`
	DashboardToken         string             `json:"dashboardToken,omitempty"`
	DashboardTokenEnc      *SecretValue       `json:"dashboardTokenEnc,omitempty"`
	Tokens                 []AccessToken      `json:"tokens"`
	Groups                 []ModelGroupConfig `json:"modelGroups"`
	Responses              ResponsesConfig    `json:"responses,omitempty"`              // Responses API 兼容策略
	Usage                  UsageConfig        `json:"usage,omitempty"`                  // 用量估算配置
	HeartbeatTimeout       int                `json:"heartbeatTimeout,omitempty"`       // 心跳超时时间（秒）
	HTTPTimeout            int                `json:"httpTimeout,omitempty"`            // HTTP 请求超时时间（秒），0 为不限制
	DebugMode              bool               `json:"debugMode,omitempty"`              // 调试模式
	VerboseLog             bool               `json:"verboseLog,omitempty"`             // 详细日志模式
	UsagePersistEnabled    *bool              `json:"usagePersistEnabled,omitempty"`    // 持久化用量统计
	UsagePersistMaxRecords int                `json:"usagePersistMaxRecords,omitempty"` // 最多保留的用量记录条数
	mu                     sync.RWMutex
	path                   string
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
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}

	cfg.mu.Lock()
	GlobalConfig = &cfg
	cfg.mu.Unlock()

	return &cfg, nil
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
	if err := newCfg.resolveSecrets(); err != nil {
		return err
	}

	c.mu.Lock()
	c.Server = newCfg.Server
	c.DashboardToken = newCfg.DashboardToken
	c.DashboardTokenEnc = newCfg.DashboardTokenEnc
	c.Tokens = newCfg.Tokens
	c.Groups = newCfg.Groups
	c.Responses = newCfg.Responses
	c.Usage = newCfg.Usage
	c.HeartbeatTimeout = newCfg.HeartbeatTimeout
	c.HTTPTimeout = newCfg.HTTPTimeout
	c.DebugMode = newCfg.DebugMode
	c.VerboseLog = newCfg.VerboseLog
	c.UsagePersistEnabled = newCfg.UsagePersistEnabled
	c.UsagePersistMaxRecords = newCfg.UsagePersistMaxRecords
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
		if item.Enabled && item.Token == token {
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
	c.mu.RUnlock()

	return dashboardToken != "" && token == dashboardToken
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

func (c *Config) GetHeartbeatTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.HeartbeatTimeout > 0 {
		return time.Duration(c.HeartbeatTimeout) * time.Second
	}
	return 300 * time.Second // 默认 300 秒
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
		cfg.UpstreamMode = "native"
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
