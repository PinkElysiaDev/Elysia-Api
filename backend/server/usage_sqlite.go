package server

import (
	"context"
	"encoding/json"

	"github.com/elysia-api/backend/storage"
)

func (s *Server) saveUsageRecordToStore(record *usageRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	summary := storage.UsageLogItem{
		RequestID:         record.RequestID,
		StartedAt:         record.StartedAt,
		KeyName:           record.KeyName,
		KeyHash:           record.KeyHash,
		GroupName:         record.GroupName,
		ModelName:         record.ModelName,
		Platform:          record.Platform,
		SourceFormat:      record.SourceFormat,
		TargetFormat:      record.TargetFormat,
		RelayMode:         record.RelayMode,
		ResponsesMode:     record.ResponsesMode,
		UsageSource:       record.UsageSource,
		Stream:            record.Stream,
		StatusCode:        record.StatusCode,
		Error:             record.Error,
		FirstByteMs:       record.FirstByteMs,
		DurationMs:        record.DurationMs,
		InputTokens:       intPtrValue(record.Usage.InputTokens),
		OutputTokens:      intPtrValue(record.Usage.OutputTokens),
		TotalTokens:       intPtrValue(record.Usage.TotalTokens),
		CacheHitTokens:    intPtrValue(record.Usage.CacheHitTokens),
		RequestTruncated:  record.IncomingBody.Truncated,
		ResponseTruncated: record.ProviderResponse.Truncated,
	}
	return s.store.SaveUsageRecordJSON(context.Background(), payload, summary, record.EndedAt)
}

func intPtrValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
