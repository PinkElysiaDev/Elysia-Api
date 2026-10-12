package server

import (
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
)

// updateRuntimeConfig publishes side effects only after the complete candidate
// is durable. CLI updates and HTTP updates use the same commit boundary.
func (s *Server) updateRuntimeConfig(payload runtimeConfigPayload) (bool, error) {
	if err := validateRuntimeConfigPayload(&payload); err != nil {
		return false, err
	}
	s.configUpdateMu.Lock()
	defer s.configUpdateMu.Unlock()
	beforeServer := s.config.GetServer()
	beforeDatabase := s.config.GetDatabasePath()
	beforePprof := s.config.GetEnablePprof()
	beforeUsage := s.config.GetUsageLogConfig()
	beforeSystem := s.config.GetSystemLogConfig()
	if err := s.config.Update(func(candidate *config.Config) error {
		applyRuntimeConfig(candidate, payload)
		return nil
	}); err != nil {
		return false, err
	}
	if payload.HTTPTimeout != nil {
		s.protocolTransport.SetTimeout(time.Duration(s.config.GetHTTPTimeout()) * time.Second)
	}
	if payload.Outbound != nil {
		s.syncOutboundPolicy()
	}
	hasRestartChange := beforeServer != s.config.GetServer() || beforeDatabase != s.config.GetDatabasePath() || beforePprof != s.config.GetEnablePprof()
	if hasRestartChange {
		s.markRestartRequired()
	}
	if retentionTightened(beforeUsage, s.config.GetUsageLogConfig(), beforeSystem, s.config.GetSystemLogConfig()) {
		s.usageRetention.triggerAsync()
	}
	return hasRestartChange, nil
}

func applyRuntimeConfig(candidate *config.Config, payload runtimeConfigPayload) {
	if payload.LogLevel != "" {
		candidate.SetLogLevel(payload.LogLevel)
	}
	if payload.HTTPTimeout != nil {
		candidate.SetHTTPTimeout(*payload.HTTPTimeout)
	}
	if payload.PanelAccessToken != nil {
		candidate.SetPanelAccessToken(*payload.PanelAccessToken)
	}
	if payload.DatabasePath != nil {
		candidate.SetDatabasePath(*payload.DatabasePath)
	}
	if payload.EnablePprof != nil {
		candidate.SetEnablePprof(*payload.EnablePprof)
	}
	if payload.Outbound != nil {
		ranges := make([]string, len(payload.Outbound.DeniedIPRanges))
		for i, entry := range payload.Outbound.DeniedIPRanges {
			ranges[i] = strings.TrimSpace(entry)
		}
		candidate.SetOutboundDeniedIPRanges(ranges)
	}
	if payload.UsageLog != nil {
		candidate.SetUsageLogConfig(*payload.UsageLog)
	}
	if payload.SystemLog != nil {
		candidate.SetSystemLogConfig(*payload.SystemLog)
	}
	if payload.ModelCatalog != nil && payload.ModelCatalog.SyncIntervalMinutes != nil {
		candidate.SetModelCatalogSyncInterval(*payload.ModelCatalog.SyncIntervalMinutes)
	}
	if payload.Agent != nil && payload.Agent.ToolLoopLimit != nil {
		candidate.SetAgentToolLoopLimit(*payload.Agent.ToolLoopLimit)
	}
	if payload.AgentRemote != nil {
		remote := candidate.GetAgentRemote()
		if payload.AgentRemote.Enabled != nil {
			remote.Enabled = payload.AgentRemote.Enabled
		}
		if payload.AgentRemote.PublicURL != nil {
			remote.PublicURL = strings.TrimSpace(*payload.AgentRemote.PublicURL)
		}
		candidate.SetAgentRemote(remote)
	}
	if payload.Host != "" {
		candidate.SetHost(payload.Host)
	}
	if payload.Port != 0 {
		candidate.SetPort(payload.Port)
	}
}

func isLimitTighter(before, after int64) bool {
	return after > 0 && (before == 0 || after < before)
}

func retentionTightened(beforeUsage, afterUsage config.UsageLogResolved, beforeSystem, afterSystem config.LogRetentionResolved) bool {
	return isLimitTighter(int64(beforeUsage.RetentionDays), int64(afterUsage.RetentionDays)) ||
		isLimitTighter(beforeUsage.MaxContentBytes, afterUsage.MaxContentBytes) ||
		isLimitTighter(int64(beforeUsage.MaxRecords), int64(afterUsage.MaxRecords)) ||
		isLimitTighter(int64(beforeSystem.RetentionDays), int64(afterSystem.RetentionDays)) ||
		isLimitTighter(int64(beforeSystem.MaxContentMB), int64(afterSystem.MaxContentMB)) ||
		isLimitTighter(int64(beforeSystem.MaxRecords), int64(afterSystem.MaxRecords))
}
