package server

import "time"

const (
	AffinityTTL          = 5 * time.Minute
	UsageBodyMaxBytes    = 1 * 1024 * 1024
	DefaultCharsPerToken = 4
	HealthProbeMaxTokens = 1
	RetryErrorMaxLen     = 512
)

// ErrorKind* 是 usage 记录 errorKind 字段的归类值（供面板筛选/展示）。
// 未归类的失败保持空串，不参与前端徽标渲染。
const (
	ErrorKindConversion = "conversion" // 协议转换失败（OpenAI/Claude/Gemini 互转）
	ErrorKindUpstream   = "upstream"   // 上游转发失败/候选耗尽
)

// RelayMode / ResponsesMode 是 usage 记录的序列化字段值（统计侧按字面比对，
// 拼错即统计失真），统一在此定义。
const (
	RelayModePassthrough     = "passthrough"
	RelayModeTransform       = "transform"
	ResponsesModeNative      = "native_responses"
	ResponsesModeTransformed = "transformed_responses"
	CacheHeaderImmutable     = "public, max-age=31536000, immutable"
	UsageLogsDefaultPageSize = 50
	UsageLogsMaxPageSize     = 500
	StreamEventsCacheMax     = 50
)
