package storage

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Store struct {
	db    *sql.DB
	codec *secretCodec
	path  string

	// rollupReady：小时级预聚合已完成回填，聚合查询可走 rollup 路径。
	rollupReady atomic.Bool
	rollupMu    sync.Mutex // 序列化回填执行（防重复启动）
	rollupWG    sync.WaitGroup
	// rollupCtx 在 Close 时取消，让后台回填在关库前退出。
	rollupCtx    context.Context
	rollupCancel context.CancelFunc
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
	// CacheSynthesis 让网关为声明 cache.breakpoints 的上游补结构断点。
	// 默认关闭：开启会改变发往上游的请求体，属显式的运维选择。
	CacheSynthesis bool      `json:"cacheSynthesis,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// EffectiveKeys 过滤停用和空 Key；仅未配置 Key 池时回退 APIKey。
func (s ModelSource) EffectiveKeys() []SourceAPIKey {
	enabled := make([]SourceAPIKey, 0, len(s.APIKeys))
	for _, key := range s.APIKeys {
		if !key.Disabled && strings.TrimSpace(key.Value) != "" {
			if s.AutoFetchModels {
				key.AllowedModels = nil
			} else {
				key.FetchedModels = nil
			}
			enabled = append(enabled, key)
		}
	}
	if len(s.APIKeys) > 0 {
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
	// FetchedModels 是发现集；nil 表示尚未发现，空数组表示无可用模型。
	FetchedModels []string `json:"fetchedModels"`
	// AllowedModels 仅用于手动源的模型分配；nil 不限制，空数组表示全部禁用。
	AllowedModels []string `json:"allowedModels"`
}

// KeyAllowsModel 匹配 Key 的有效模型集合；nil 表示未限制。
func (k SourceAPIKey) KeyAllowsModel(modelID string) bool {
	return (k.FetchedModels == nil || slices.Contains(k.FetchedModels, modelID)) &&
		(k.AllowedModels == nil || slices.Contains(k.AllowedModels, modelID))
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

// Identifier 返回调用 ID；ID 缺失时使用 Name 兼容历史模型。
func (m Model) Identifier() string {
	if m.ID != "" {
		return m.ID
	}
	return m.Name
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
	Name          string   `json:"name"`
	Token         string   `json:"token,omitempty"`
	Enabled       bool     `json:"enabled"`
	AllowedGroups []string `json:"allowedGroups"` // 允许访问的模型组名称；空表示不限制（可访问全部）
	// Scopes 是端点级作用域（如 "agent"=可控制 AI 助手）。空表示只能
	// 调用推理接口——与 allowedGroups（模型组维度）正交。
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TokenScopeAgent 允许控制 AI 助手（REST/MCP/A2A 三个远程面）。
const TokenScopeAgent = "agent"

// NormalizeScopes 清洗作用域列表：去空白、去重、丢弃未知值——写路径统一
// 调用，保证落库的 scopes 只含认识的键。
func NormalizeScopes(scopes []string) []string {
	seen := make(map[string]bool, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope != TokenScopeAgent || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out
}

// HasScope 报告令牌是否具备指定作用域。
func (t APIToken) HasScope(scope string) bool {
	for _, item := range t.Scopes {
		if item == scope {
			return true
		}
	}
	return false
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
	Date            string `json:"date"`
	Requests        int    `json:"requests"`
	SuccessRequests int    `json:"successRequests"`
	FailedRequests  int    `json:"failedRequests"`
	InputTokens     int    `json:"inputTokens,omitempty"`
	OutputTokens    int    `json:"outputTokens,omitempty"`
	CacheHitTokens  int    `json:"cacheHitTokens,omitempty"`
	// CacheCreationTokens 是缓存创建 token 数；未上报的行贡献 0（见 UsageReport*
	// 掩码）。与 cacheHitTokens 同口径，只累计成功记录。
	CacheCreationTokens int            `json:"cacheCreationTokens,omitempty"`
	Tokens              int            `json:"tokens"`
	ModelTokens         map[string]int `json:"modelTokens,omitempty"`
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

// usage_report_mask 位含义：记录该行的计数是否来自上游真实上报。缺位 = 上游
// 未报告（不得当作零），置位 = 上游确实报告了该计数（可能是零）。命中率的
// 分子分母只有在相应位置位时才可比。
const (
	UsageReportInput    = 1 << 0 // input_tokens 来自上游上报
	UsageReportCacheHit = 1 << 1 // cache_hit_tokens 来自上游上报
	UsageReportCreation = 1 << 2 // cache_creation_tokens 来自上游上报
)

type UsageLogItem struct {
	RequestID           string    `json:"requestId"`
	StartedAt           time.Time `json:"startedAt"`
	KeyName             string    `json:"keyName"`
	KeyHash             string    `json:"keyHash"`
	RequestedModelGroup string    `json:"requestedModelGroup"`
	GroupName           string    `json:"groupName"`
	ModelName           string    `json:"modelName"`
	SourceID            string    `json:"sourceId,omitempty"`
	Platform            string    `json:"platform"`
	SourceFormat        string    `json:"sourceFormat"`
	TargetFormat        string    `json:"targetFormat"`
	RelayMode           string    `json:"relayMode"`
	ResponsesMode       string    `json:"responsesMode"`
	UsageSource         string    `json:"usageSource"`
	Stream              bool      `json:"stream"`
	StatusCode          int       `json:"statusCode"`
	Error               string    `json:"error,omitempty"`
	FirstByteMs         int64     `json:"firstByteMs"`
	DurationMs          int64     `json:"durationMs"`
	InputTokens         int       `json:"inputTokens"`
	OutputTokens        int       `json:"outputTokens"`
	TotalTokens         int       `json:"totalTokens"`
	CacheHitTokens      int       `json:"cacheHitTokens"`
	// CacheCreationTokens 是缓存创建 token 数；仅当 UsageReportMask 的
	// UsageReportCreation 位置位时才代表上游上报值。历史行保持 0 且未置位。
	CacheCreationTokens int `json:"cacheCreationTokens"`
	// UsageReportMask 见 UsageReport* 位常量：区分「上游未上报」与「上报为零」。
	UsageReportMask   int  `json:"usageReportMask"`
	RequestTruncated  bool `json:"incomingBodyTruncated"`
	ResponseTruncated bool `json:"providerResponseTruncated"`
}

type SystemLog struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Fields    string    `json:"fields,omitempty"`
}
