package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elysia-api/backend/internal/dbpath"
	"github.com/elysia-api/backend/logging"
)

// ErrDatabasePath identifies invalid database paths independently of HTTP errors.
var ErrDatabasePath = errors.New("invalid database path")

// LogRetentionConfig uses content budgets, not a physical SQLite file limit.
type LogRetentionConfig struct {
	RetentionDays *int `json:"retentionDays,omitempty"`
	MaxRecords    *int `json:"maxRecords,omitempty"`
	MaxContentMB  *int `json:"maxContentMB,omitempty"`
}

type LogRetentionResolved struct {
	RetentionDays int `json:"retentionDays"`
	MaxRecords    int `json:"maxRecords"`
	MaxContentMB  int `json:"maxContentMB"`
}

func (c *Config) GetSystemLogConfig() LogRetentionResolved {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return LogRetentionResolved{limitOrDefault(c.SystemLog.RetentionDays, 30), positiveOr(c.SystemLog.MaxRecords, 0), limitOrDefault(c.SystemLog.MaxContentMB, 256)}
}

func limitOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return max(*value, 0)
}

func (c *Config) GetLogDatabasePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.LogDatabasePath != "" {
		return c.LogDatabasePath
	}
	return dbpath.DefaultLogPath(c.DatabasePath)
}

func (c *Config) SetLogDatabasePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path = strings.TrimSpace(path)
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(c.path), path)
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
	}
	c.LogDatabasePath = path
}

func (c *Config) GetLoggingOptions() logging.Options {
	c.mu.RLock()
	defer c.mu.RUnlock()
	format, persist := c.LogFormat, c.LogPersistLevel
	if format == "" {
		format = "auto"
	}
	if persist == "" {
		persist = "info"
	}
	return logging.Options{Level: c.LogLevel, Format: format, PersistLevel: persist}
}

func (c *Config) SetLogOutput(format, persist string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if format != "" {
		c.LogFormat = format
	}
	if persist != "" {
		c.LogPersistLevel = persist
	}
}

func (c *Config) validateLogSettings() error {
	for name, level := range map[string]string{"logLevel": c.LogLevel, "logPersistLevel": c.LogPersistLevel} {
		switch level {
		case "", "debug", "info", "warn", "error":
		default:
			return fmt.Errorf("%s must be debug/info/warn/error", name)
		}
	}
	switch c.LogFormat {
	case "", "auto", "json", "console":
	default:
		return fmt.Errorf("logFormat must be auto/json/console")
	}
	if err := dbpath.Validate(c.DatabasePath, c.GetLogDatabasePath()); err != nil {
		return fmt.Errorf("%w: %w", ErrDatabasePath, err)
	}
	return nil
}

func (c *Config) SetSystemLogConfig(p LogRetentionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.RetentionDays != nil {
		c.SystemLog.RetentionDays = clampIntPtr(p.RetentionDays)
	}
	if p.MaxRecords != nil {
		c.SystemLog.MaxRecords = clampIntPtr(p.MaxRecords)
	}
	if p.MaxContentMB != nil {
		c.SystemLog.MaxContentMB = clampIntPtr(p.MaxContentMB)
	}
}
