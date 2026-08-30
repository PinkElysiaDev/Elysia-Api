package storage

import (
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Store struct {
	db    *sql.DB
	codec *secretCodec

	// rollupReady：小时级预聚合已完成回填，聚合查询可走 rollup 路径。
	rollupReady atomic.Bool
	rollupMu    sync.Mutex // 序列化回填执行（防重复启动）
	rollupWG    sync.WaitGroup
}

type ModelSource struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	BaseURL         string  `json:"baseUrl"`
	APIKey          string  `json:"apiKey,omitempty"`
	Platform        string  `json:"platform"`
	Enabled         bool    `json:"enabled"`
	AutoFetchModels bool    `json:"autoFetchModels"`
	ManualModels    []Model `json:"manualModels,omitempty"`
	// FetchBaseURL 为模型列表拉取专用地址（方向5）：空 = 与 BaseURL 一致（现状）。
	// 用于拉取端点与请求端点不同源（域名/端口/协议不一致）的站点。
	FetchBaseURL string `json:"fetchBaseUrl,omitempty"`
	// APIKeys 为多 Key 配置（方向6）：空列表 = 单 key 模式（用 APIKey 字段）。
	// 存储 JSON 数组整体加密；KeyStrategy 决定调度方式。
	APIKeys     []SourceAPIKey    `json:"apiKeys,omitempty"`
	KeyStrategy SourceKeyStrategy `json:"keyStrategy,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// EffectiveKeys 返回参与调度的 key 列表（多 key 时过滤 disabled；单 key 回退 APIKey）。
// 不做策略选择，策略由运行时调度器按 KeyStrategy 消费。
func (s ModelSource) EffectiveKeys() []SourceAPIKey {
	enabled := make([]SourceAPIKey, 0, len(s.APIKeys))
	for _, key := range s.APIKeys {
		if !key.Disabled && strings.TrimSpace(key.Value) != "" {
			enabled = append(enabled, key)
		}
	}
	if len(enabled) > 0 {
		return enabled
	}
	if strings.TrimSpace(s.APIKey) != "" {
		return []SourceAPIKey{{Value: s.APIKey}}
	}
	return nil
}

// SourceAPIKey 是模型源多 Key 配置中的一条（方向6）。
type SourceAPIKey struct {
	Value    string `json:"value"`
	Note     string `json:"note,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// FetchedModels 是该 key 上次独立拉取到的模型集——站点分组权限的自动发现
	// 结果，同时作为前端勾选界面（启用/停用该 key 可服务的模型）的展示宇宙。
	// nil = 从未按 key 拉取过。
	FetchedModels []string `json:"fetchedModels,omitempty"`
	// AllowedModels 是用户勾选启用的模型子集。nil = 未做过勾选（启用全部
	// FetchedModels）；非 nil 时仅启用其中的模型（空数组 = 全部停用，是合法的
	// 显式选择，故不用 omitempty——否则 [] 会被吞成 nil 语义）。手动模式下由
	// 「模型 ↔ key」多选在保存时直接编译写入本字段。
	AllowedModels []string `json:"allowedModels"`
}

// KeyAllowsModel 判断该 key 是否可服务指定模型：
//   - AllowedModels 非 nil：按启用集精确匹配；
//   - 否则 FetchedModels 非 nil：按拉取集匹配（未勾选 = 全启用）；
//   - 两者皆 nil（从未按 key 拉取/配置）：不限制。
func (k SourceAPIKey) KeyAllowsModel(modelID string) bool {
	if k.AllowedModels != nil {
		for _, id := range k.AllowedModels {
			if id == modelID {
				return true
			}
		}
		return false
	}
	if k.FetchedModels != nil {
		for _, id := range k.FetchedModels {
			if id == modelID {
				return true
			}
		}
		return false
	}
	return true
}

// SourceKeyStrategy 是源级 key 调度策略。
//
//	single（默认，兼容旧单 key）/ round-robin / random / priority（按列表顺序，失败先轮换 key）。
type SourceKeyStrategy string

const (
	KeyStrategySingle     SourceKeyStrategy = "single"
	KeyStrategyRoundRobin SourceKeyStrategy = "round-robin"
	KeyStrategyRandom     SourceKeyStrategy = "random"
	KeyStrategyPriority   SourceKeyStrategy = "priority"
)

type Model struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SourceID         string `json:"sourceId,omitempty"`
	SourceName       string `json:"sourceName,omitempty"`
	BaseURL          string `json:"baseUrl"`
	APIKey           string `json:"apiKey,omitempty"`
	Platform         string `json:"platform"`
	Type             string `json:"type"`
	MaxTokens        int    `json:"maxTokens"`
	VisionCapable    bool   `json:"visionCapable"`
	ToolsCapable     bool   `json:"toolsCapable"`
	StructuredOutput bool   `json:"structuredOutput"`
	ThinkingMode     string `json:"thinkingMode"`
	Available        bool   `json:"available"`
	// Enabled 是用户手动启停开关（方向4），与 Available（健康检测自动翻转）分离：
	// 模型可被调度 = Enabled && Available。
	Enabled bool `json:"enabled"`
	// Origin 标记行的来源：fetched（上游拉取，随刷新合并替换）/ manual
	// （手动模型或用户显式创建，刷新永不触碰、上游消失也不删）。
	Origin string `json:"origin"`
	// CapabilitySource 标记能力字段的填充来源：''（未知/默认）、'catalog'
	// （models.dev 目录自动回填，刷新可覆盖）、'manual'（用户手改，刷新保留）。
	CapabilitySource string    `json:"capabilitySource"`
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
	From      time.Time
	To        time.Time
	Limit     int
	Offset    int
	KeyName   string
	KeyHash   string
	GroupName string
	ModelName string
	// Status 可选 success / failed；StatusCode 非零时精确状态码优先。
	Status     string
	StatusCode int
	// 多选筛选：非空时优先于对应的单值字段，生成 IN (...) 条件。
	KeyNames   []string
	GroupNames []string
	ModelNames []string
	SourceID   string
	SourceIDs  []string
	// orphanTimestamps：只查 started_ms<=0 的坏时间戳行（内部使用，不对外暴露）。
	orphanTimestamps bool
}

// UsageDailyBucket 趋势图的单日聚合行。Date 是请求方时区的本地日（YYYY-MM-DD）。
type UsageDailyBucket struct {
	Date            string         `json:"date"`
	Requests        int            `json:"requests"`
	SuccessRequests int            `json:"successRequests"`
	FailedRequests  int            `json:"failedRequests"`
	InputTokens     int            `json:"inputTokens,omitempty"`
	OutputTokens    int            `json:"outputTokens,omitempty"`
	CacheHitTokens  int            `json:"cacheHitTokens,omitempty"`
	Tokens          int            `json:"tokens"`
	ModelTokens     map[string]int `json:"modelTokens,omitempty"`
}

// UsageModelBucket 按模型的聚合行（热门模型 / 明细表）。
type UsageModelBucket struct {
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	Failed   int    `json:"failed"`
	Tokens   int    `json:"tokens"`
}

// UsagePulsePoint 短窗脉搏的一个时间桶（RPM / 时延）。
// T 是该桶起始时刻的 Unix 毫秒（与 utcOffsetMinutes 对齐后再折回 UTC）。
type UsagePulsePoint struct {
	T             int64   `json:"t"`
	Requests      int     `json:"requests"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	P95DurationMs float64 `json:"p95DurationMs"`
	TotalTokens   int64   `json:"totalTokens,omitempty"`
}

// UsagePulseWindow 是整段 [from, to) 窗口的汇总（请求数 / 平均耗时为全量；
// P95 在样本 ≤ 16384 时精确，超出为蓄水池估算，不是各桶 P95 的加权平均）。
type UsagePulseWindow struct {
	Requests      int     `json:"requests"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	P95DurationMs float64 `json:"p95DurationMs"`
	TotalTokens   int64   `json:"totalTokens"`
}

// UsagePulseResult 短窗脉搏：分桶序列 + 窗口级汇总。
type UsagePulseResult struct {
	Points []UsagePulsePoint `json:"points"`
	Window UsagePulseWindow  `json:"window"`
}

// UsageModelDailyBucket 某本地日某个模型的请求数。
// Other 为 true 时表示 Top N 之外的合计，Model 为空，展示文案由调用方决定。
type UsageModelDailyBucket struct {
	Date     string `json:"date"`
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	Other    bool   `json:"isOther,omitempty"`
}

type UsageLogItem struct {
	RequestID         string    `json:"requestId"`
	StartedAt         time.Time `json:"startedAt"`
	KeyName           string    `json:"keyName"`
	KeyHash           string    `json:"keyHash"`
	GroupName         string    `json:"groupName"`
	ModelName         string    `json:"modelName"`
	SourceID          string    `json:"sourceId,omitempty"`
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
