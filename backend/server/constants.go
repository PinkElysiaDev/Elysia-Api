package server

import "time"

const (
	AffinityTTL          = 5 * time.Minute
	UsageBodyMaxBytes    = 1 * 1024 * 1024
	DefaultCharsPerToken = 4
	HealthProbeMaxTokens = 1
	RetryErrorMaxLen     = 512
)

// RelayMode / ResponsesMode 是 usage 记录的序列化字段值（统计侧按字面比对，
// 拼错即统计失真），统一在此定义。
const (
	RelayModePassthrough        = "passthrough"
	RelayModeTransform          = "transform"
	ResponsesModeNative         = "native_responses"
	ResponsesModeTransformed    = "transformed_responses"
	CacheHeaderImmutable        = "public, max-age=31536000, immutable"
	UsageLogsDefaultPageSize    = 50
	UsageLogsMaxPageSize        = 500
	StreamEventsCacheMax        = 50
)
