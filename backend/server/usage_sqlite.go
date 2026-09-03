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
	// bodyOnErrorOnly：成功请求的请求体没有排查价值，四段 body 与外置媒体
	// 全部不落。错误在请求结束才确定，因此判定只能在持久化末端做——
	// 捕获期仍照常提取占位符，这里直接清空，成功请求零磁盘写。
	if cfg.BodyOnErrorOnly && record.Error == "" {
		record.IncomingBody = usageBody{}
		record.OutgoingBody = usageBody{}
		record.ProviderResponse = usageBody{}
		record.DownstreamResponse = usageBody{}
	} else if record.assets.count() > 0 {
		if _, err := writeUsageAssets(s.usageAssetsRoot(), record.assets.items); err != nil {
			// 资产写盘失败不阻断记录落库：占位符已写入 body，管理端
			// 取不到文件时按 404 呈现，正文其余部分仍可排查。
			log.Printf("usage assets: failed to write %d assets for %s: %v", record.assets.count(), record.RequestID, err)
		} else {
			record.RequestWarnings = append(record.RequestWarnings,
				fmt.Sprintf("%d media assets externalized", record.assets.count()))
		}
		// 引用计数：文件全局去重后，删除时机由「还有没有记录引用」决定。
		// 单条失败不阻断（缺引用的文件交由孤儿清扫在宽限期后回收）。
		for _, item := range record.assets.items {
			file := item.Hash + "." + item.Ext
			if err := s.store.InsertUsageAssetRef(context.Background(), record.RequestID, file); err != nil {
				log.Printf("usage assets: failed to ref %s for %s: %v", file, record.RequestID, err)
			}
		}
	}
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
		SourceID:          record.SourceID,
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
		InputTokens:       getInt(record.Usage.InputTokens),
		OutputTokens:      getInt(record.Usage.OutputTokens),
		TotalTokens:       getInt(record.Usage.TotalTokens),
		CacheHitTokens:    getInt(record.Usage.CacheHitTokens),
		RequestTruncated:  record.IncomingBody.Truncated,
		ResponseTruncated: record.ProviderResponse.Truncated,
	}
	return s.store.SaveUsageRecordJSON(context.Background(), payload, summary, record.EndedAt)
}

// usageLogConfig 返回本服务生效的日志策略；无 config 的裸 Server（测试）
// 走全默认，保持与历史行为一致。
func (s *Server) usageLogConfig() config.UsageLogResolved {
	if s.config == nil {
		return config.DefaultUsageLogResolved()
	}
	return s.config.GetUsageLogConfig()
}

// usageAssetsRoot 返回媒体资产根目录（数据库同目录下的 usage-assets/）。
func (s *Server) usageAssetsRoot() string {
	if s.config == nil {
		return ""
	}
	dbPath := s.config.GetDatabasePath()
	if dbPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dbPath), usageAssetsDirName)
}
