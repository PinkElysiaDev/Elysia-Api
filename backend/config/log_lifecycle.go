package config

import (
	"encoding/json"
	"fmt"
	"os"
)

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

// migrateLogConfig is the only reader of retired log options. The atomic file
// replacement is the phase commit; retrying after a crash never overwrites backup.
func migrateLogConfig(path string, data []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("configuration must be a JSON object")
	}
	var version int
	if err := json.Unmarshal(raw["logLifecycleVersion"], &version); err == nil && version >= 1 {
		return data, nil
	}
	usage := map[string]json.RawMessage{}
	if v := raw["usageLog"]; len(v) > 0 && string(v) != "null" {
		if err := json.Unmarshal(v, &usage); err != nil {
			return nil, err
		}
	}
	for old, key := range map[string]string{"usagePersistEnabled": "persistEnabled", "usagePersistMaxRecords": "maxRecords"} {
		if _, exists := usage[key]; !exists && raw[old] != nil {
			usage[key] = raw[old]
		}
		delete(raw, old)
	}
	if _, exists := usage["maxContentMB"]; !exists && usage["maxStorageMB"] != nil {
		usage["maxContentMB"] = usage["maxStorageMB"]
	}
	delete(usage, "maxStorageMB")
	if len(usage) > 0 {
		raw["usageLog"], _ = json.Marshal(usage)
	}
	raw["logLifecycleVersion"] = json.RawMessage("1")
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	var validated Config
	if err := json.Unmarshal(out, &validated); err != nil {
		return nil, err
	}
	backup := path + ".pre-log-lifecycle"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if err := WriteFileAtomic(backup, data, 0o600); err != nil {
			return nil, fmt.Errorf("backup log config: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	if err := WriteFileAtomic(path, out, 0o600); err != nil {
		return nil, err
	}
	return out, nil
}
