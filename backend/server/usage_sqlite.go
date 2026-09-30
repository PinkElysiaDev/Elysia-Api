package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) saveUsageRecordToStore(record *usageRecord) error {
	cfg := s.usageLogConfig()
	var assets []storage.UsageAsset
	// bodyOnErrorOnly：成功请求的请求体没有排查价值，四段 body 与外置媒体
	// 全部不落。错误在请求结束才确定，因此判定只能在持久化末端做——
	// 捕获期仍照常提取占位符，这里直接清空，成功请求零磁盘写。
	if cfg.BodyOnErrorOnly && record.Error == "" {
		record.IncomingBody = usageBody{}
		record.OutgoingBody = usageBody{}
		record.ProviderResponse = usageBody{}
		record.DownstreamResponse = usageBody{}
	} else if record.assets.count() > 0 {
		var err error
		assets, err = writeUsageAssets(s.usageAssetsRoot(), record.assets.items)
		if err != nil {
			record.RequestWarnings = append(record.RequestWarnings, "media attachment missing: "+err.Error())
			log.Printf("usage assets: failed to write assets for %s: %v", record.RequestID, err)
		}
		if len(assets) > 0 {
			record.RequestWarnings = append(record.RequestWarnings, fmt.Sprintf("%d media assets externalized", len(assets)))
		}
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	summary := storage.UsageLogItem{
		RequestID:           record.RequestID,
		StartedAt:           record.StartedAt,
		KeyName:             record.KeyName,
		KeyHash:             record.KeyHash,
		RequestedModelGroup: record.RequestedModelGroup,
		GroupName:           record.GroupName,
		ModelName:           record.ModelName,
		SourceID:            record.SourceID,
		Platform:            record.Platform,
		SourceFormat:        record.SourceFormat,
		TargetFormat:        record.TargetFormat,
		RelayMode:           record.RelayMode,
		ResponsesMode:       record.ResponsesMode,
		UsageSource:         record.UsageSource,
		Stream:              record.Stream,
		StatusCode:          record.StatusCode,
		Error:               record.Error,
		FirstByteMs:         record.FirstByteMs,
		DurationMs:          record.DurationMs,
		InputTokens:         derefInt(record.Usage.InputTokens),
		OutputTokens:        derefInt(record.Usage.OutputTokens),
		TotalTokens:         derefInt(record.Usage.TotalTokens),
		CacheHitTokens:      derefInt(record.Usage.CacheHitTokens),
		RequestTruncated:    record.IncomingBody.Truncated,
		ResponseTruncated:   record.ProviderResponse.Truncated,
	}
	return s.store.SaveUsageRecordJSON(context.Background(), payload, summary, record.EndedAt, assets...)
}

// usageLogConfig 返回本服务生效的日志策略；无 config 的裸 Server（测试）
// 走全默认，仅保存元数据。
func (s *Server) usageLogConfig() config.UsageLogResolved {
	if s.config == nil {
		return config.DefaultUsageLogResolved()
	}
	return s.config.GetUsageLogConfig()
}

// usageAssetsRoot 返回媒体资产根目录（数据库同目录下的 usage-assets/）。
func (s *Server) usageAssetsRoot() string {
	if s.store != nil {
		return s.store.UsageAssetsRoot()
	}
	if s.config == nil {
		return ""
	}
	dbPath := s.config.GetDatabasePath()
	if dbPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dbPath), usageAssetsDirName)
}
