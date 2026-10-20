package config

import (
	"fmt"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// PreparationConfig is a startup policy. Changing it requires a server restart;
// model-group concurrency/daily quotas remain separate and are not replaced.
type PreparationConfig struct {
	Enabled              *bool `json:"enabled,omitempty"`
	MaxConcurrent        int   `json:"maxConcurrent,omitempty"`
	MaxQueued            *int  `json:"maxQueued,omitempty"`
	MaxReservedBodyBytes int64 `json:"maxReservedBodyBytes,omitempty"`
	QueueTimeoutMillis   int   `json:"queueTimeoutMillis,omitempty"`
}

type PreparationLimits struct {
	Enabled                  bool
	MaxConcurrent, MaxQueued int
	MaxReservedBodyBytes     int64
	QueueTimeout             time.Duration
}

func (c *Config) validatePreparation() error {
	p := c.Preparation
	if p.MaxConcurrent < 0 || p.MaxConcurrent > 1024 || p.MaxQueued != nil && (*p.MaxQueued < 0 || *p.MaxQueued > 65536) || p.QueueTimeoutMillis < 0 || p.QueueTimeoutMillis > 600000 {
		return fmt.Errorf("invalid preparation concurrency, queue size or queue timeout")
	}
	if p.MaxReservedBodyBytes < 0 {
		return fmt.Errorf("preparation body reservation must not be negative")
	}
	if p.MaxReservedBodyBytes > 0 {
		return protocol.ValidateRequestBodyLimit(p.MaxReservedBodyBytes)
	}
	return nil
}

func (c *Config) GetPreparationLimits() PreparationLimits {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := c.Preparation
	limits := PreparationLimits{Enabled: true, MaxConcurrent: 8, MaxQueued: 32, MaxReservedBodyBytes: max(64<<20, c.MaxBodyBytes), QueueTimeout: 5 * time.Second}
	if p.Enabled != nil {
		limits.Enabled = *p.Enabled
	}
	if p.MaxConcurrent > 0 {
		limits.MaxConcurrent = p.MaxConcurrent
	}
	if p.MaxQueued != nil {
		limits.MaxQueued = max(0, *p.MaxQueued)
	}
	if p.MaxReservedBodyBytes > 0 {
		limits.MaxReservedBodyBytes = p.MaxReservedBodyBytes
	}
	if p.QueueTimeoutMillis > 0 {
		limits.QueueTimeout = time.Duration(p.QueueTimeoutMillis) * time.Millisecond
	}
	return limits
}

// SetPreparation configures a new limiter before its first request. Existing
// server instances keep their frozen startup policy until restart.
func (c *Config) SetPreparation(value PreparationConfig) {
	if value.Enabled != nil {
		enabled := *value.Enabled
		value.Enabled = &enabled
	}
	if value.MaxQueued != nil {
		queued := *value.MaxQueued
		value.MaxQueued = &queued
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Preparation = value
}
