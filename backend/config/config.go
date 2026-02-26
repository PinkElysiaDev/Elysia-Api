package config

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"sync"
	"time"
)

type Config struct {
	Server           ServerConfig       `json:"server"`
	Tokens           []AccessToken      `json:"tokens"`
	Groups           []ModelGroupConfig `json:"modelGroups"`
	HeartbeatTimeout int                `json:"heartbeatTimeout,omitempty"` // 心跳超时时间（秒）
	mu               sync.RWMutex
	path             string
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type AccessToken struct {
	Token   string `json:"token"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type ModelGroupConfig struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Enabled       bool       `json:"enabled"`
	Models        []ModelRef `json:"models"`
	Strategy      string     `json:"strategy"`
	MaxRetries    int        `json:"maxRetries"`
	RetryInterval int        `json:"retryInterval"`
	MaxConcurrency int       `json:"maxConcurrency"`
	DailyLimit    DailyLimit `json:"dailyLimit"`
	Type          string     `json:"type"`
	MaxTokens     int        `json:"maxTokens,omitempty"`
	VisionCapable *bool      `json:"visionCapable,omitempty"`
	ToolsCapable  *bool      `json:"toolsCapable,omitempty"`
}

type ModelRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Platform string `json:"platform"`
}

type DailyLimit struct {
	Enabled    bool  `json:"enabled"`
	MaxRequest int   `json:"maxRequests"`
	MaxTokens  int   `json:"maxTokens"`
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

	c.mu.Lock()
	c.Server = newCfg.Server
	c.Tokens = newCfg.Tokens
	c.Groups = newCfg.Groups
	c.HeartbeatTimeout = newCfg.HeartbeatTimeout
	c.mu.Unlock()

	return nil
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
	for _, group := range c.Groups {
		if group.Name == name {
			return &group
		}
	}
	return nil
}

func (c *Config) GetTokens() []AccessToken {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Tokens
}

func (c *Config) GetHeartbeatTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.HeartbeatTimeout > 0 {
		return time.Duration(c.HeartbeatTimeout) * time.Second
	}
	return 300 * time.Second // 默认 300 秒
}

func init() {
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
