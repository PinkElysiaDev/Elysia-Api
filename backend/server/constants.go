package server

import "time"

const (
	AffinityTTL          = 5 * time.Minute
	UsageBodyMaxBytes    = 1 * 1024 * 1024
	DefaultCharsPerToken = 4
	HealthProbeMaxTokens = 1
	RetryErrorMaxLen     = 512
)
