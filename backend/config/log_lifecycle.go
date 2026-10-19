package config

// LogRetentionConfig uses content budgets, not a limit on the shared SQLite file.
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
	return LogRetentionResolved{positiveOr(c.SystemLog.RetentionDays, 0), positiveOr(c.SystemLog.MaxRecords, 0), positiveOr(c.SystemLog.MaxContentMB, 0)}
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
