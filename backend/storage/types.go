package storage

import (
	"database/sql"
	"time"
)

type Store struct {
	db    *sql.DB
	codec *secretCodec
}

type ModelSource struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	BaseURL         string    `json:"baseUrl"`
	APIKey          string    `json:"apiKey,omitempty"`
	Platform        string    `json:"platform"`
	Enabled         bool      `json:"enabled"`
	AutoFetchModels bool      `json:"autoFetchModels"`
	ManualModels    []Model   `json:"manualModels,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Model struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	SourceID         string    `json:"sourceId,omitempty"`
	SourceName       string    `json:"sourceName,omitempty"`
	BaseURL          string    `json:"baseUrl"`
	APIKey           string    `json:"apiKey,omitempty"`
	Platform         string    `json:"platform"`
	Type             string    `json:"type"`
	MaxTokens        int       `json:"maxTokens"`
	VisionCapable    bool      `json:"visionCapable"`
	ToolsCapable     bool      `json:"toolsCapable"`
	StructuredOutput bool      `json:"structuredOutput"`
	ThinkingMode     string    `json:"thinkingMode"`
	Available        bool      `json:"available"`
	LastCheckedAt    time.Time `json:"lastCheckedAt"`
}

type ModelGroup struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Enabled               bool     `json:"enabled"`
	Models                []string `json:"models"`
	Strategy              string   `json:"strategy"`
	MaxRetries            int      `json:"maxRetries"`
	RetryInterval         int      `json:"retryInterval"`
	MaxConcurrency        int      `json:"maxConcurrency,omitempty"`
	DailyLimitMaxRequests int      `json:"dailyLimitMaxRequests,omitempty"`
	DailyLimitMaxTokens   int      `json:"dailyLimitMaxTokens,omitempty"`
	Type                  string   `json:"type"`
	MaxTokens             int      `json:"maxTokens,omitempty"`
	VisionCapable         bool     `json:"visionCapable"`
	ToolsCapable          bool     `json:"toolsCapable"`
}

type APIToken struct {
	Name          string    `json:"name"`
	Token         string    `json:"token,omitempty"`
	Enabled       bool      `json:"enabled"`
	AllowedGroups []string  `json:"allowedGroups"` // 允许访问的模型组名称；空表示不限制（可访问全部）
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type UsageQuery struct {
	From       time.Time
	To         time.Time
	Limit      int
	Offset     int
	KeyName    string
	KeyHash    string
	GroupName  string
	ModelName  string
	StatusCode int
	// 多选筛选：非空时优先于对应的单值字段，生成 IN (...) 条件。
	KeyNames   []string
	GroupNames []string
	ModelNames []string
}

type UsageLogItem struct {
	RequestID         string    `json:"requestId"`
	StartedAt         time.Time `json:"startedAt"`
	KeyName           string    `json:"keyName"`
	KeyHash           string    `json:"keyHash"`
	GroupName         string    `json:"groupName"`
	ModelName         string    `json:"modelName"`
	Platform          string    `json:"platform"`
	SourceFormat      string    `json:"sourceFormat"`
	TargetFormat      string    `json:"targetFormat"`
	RelayMode         string    `json:"relayMode"`
	ResponsesMode     string    `json:"responsesMode"`
	UsageSource       string    `json:"usageSource"`
	Stream            bool      `json:"stream"`
	StatusCode        int       `json:"statusCode"`
	Error             string    `json:"error,omitempty"`
	FirstByteMs       int64     `json:"firstByteMs"`
	DurationMs        int64     `json:"durationMs"`
	InputTokens       int       `json:"inputTokens"`
	OutputTokens      int       `json:"outputTokens"`
	TotalTokens       int       `json:"totalTokens"`
	CacheHitTokens    int       `json:"cacheHitTokens"`
	RequestTruncated  bool      `json:"incomingBodyTruncated"`
	ResponseTruncated bool      `json:"providerResponseTruncated"`
}

type SystemLog struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Fields    string    `json:"fields,omitempty"`
}
