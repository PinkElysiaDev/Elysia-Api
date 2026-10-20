package config

import (
	"slices"

	"github.com/bytedance/sonic"
)

// Update prepares an isolated candidate and saves it before publishing settings.
// The callback must use only the candidate and must not perform external writes.
// Save, Reload and other updates cannot interleave with this transaction.
func (c *Config) Update(prepare func(candidate *Config) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	encoded, err := sonic.ConfigStd.Marshal(c)
	if err != nil {
		return err
	}
	var candidate Config
	if err := sonic.ConfigStd.Unmarshal(encoded, &candidate); err != nil {
		return err
	}
	candidate.path = c.path
	// JSON omitempty cannot preserve an explicitly empty outbound denylist.
	candidate.Outbound.DeniedIPRanges = slices.Clone(c.Outbound.DeniedIPRanges)
	if err := prepare(&candidate); err != nil {
		return err
	}
	candidate.applyEnvironmentOverrides()
	if err := candidate.validateLogSettings(); err != nil {
		return err
	}
	if err := candidate.validateProxySettings(); err != nil {
		return err
	}
	if err := candidate.validatePreparation(); err != nil {
		return err
	}
	if err := candidate.saveLocked(); err != nil {
		return err
	}
	c.publishSettingsLocked(&candidate)
	return nil
}
