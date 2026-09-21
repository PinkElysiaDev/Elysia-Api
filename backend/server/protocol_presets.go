package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 预置协议定义随二进制分发（沿 model_catalog 快照先例）；启动时逐条补齐缺失
// 的预置（ID 不在库中才写入），此后作为普通行加载——库中优先、升级不覆盖、
// 完全可编辑，「引擎在代码里，协议在数据里」。
//
//go:embed presets/*.json
var presetProtocolFS embed.FS

// PresetProtocolConfigs 解析内嵌的预置协议定义（含整体校验）。
func PresetProtocolConfigs() ([]relay.CustomProtocolConfig, error) {
	entries, err := presetProtocolFS.ReadDir("presets")
	if err != nil {
		return nil, fmt.Errorf("read embedded presets: %w", err)
	}
	configs := make([]relay.CustomProtocolConfig, 0, len(entries))
	for _, entry := range entries {
		raw, err := presetProtocolFS.ReadFile("presets/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read preset %s: %w", entry.Name(), err)
		}
		var config relay.CustomProtocolConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("parse preset %s: %w", entry.Name(), err)
		}
		if err := relay.ValidateCustomProtocol(config); err != nil {
			return nil, fmt.Errorf("preset %s is invalid: %w", entry.Name(), err)
		}
		configs = append(configs, config)
	}
	return configs, nil
}

// presetProtocolAnthropicAPIID 标记 AI 助手 few-shot 范例所用预置;
// 改名/删除该预设时此常量是唯一需要同步的位置。
const presetProtocolAnthropicAPIID = "anthropic-api"

// cachedPresetConfigs 内嵌定义不可变:解析+整体校验只做一次,助手每请求
// 复用(此前每次调用重读 embed 并校验四份)。
var cachedPresetConfigs = sync.OnceValue(func() []relay.CustomProtocolConfig {
	configs, err := PresetProtocolConfigs()
	if err != nil {
		log.Printf("custom protocol presets unavailable: %v", err)
		return nil
	}
	return configs
})

// findPresetConfig 按 ID 查找内嵌预置定义。
func findPresetConfig(id string) (relay.CustomProtocolConfig, bool) {
	for _, config := range cachedPresetConfigs() {
		if config.ID == id {
			return config, true
		}
	}
	return relay.CustomProtocolConfig{}, false
}

// customProtocolRow 由协议配置组装存储行(播种/迁移/管理写入共用)。
func customProtocolRow(config relay.CustomProtocolConfig, rawJSON string) storage.CustomProtocol {
	return storage.CustomProtocol{
		ID:      config.ID,
		Name:    config.Name,
		Version: config.Version,
		Type:    relay.NormalizeCustomProtocolType(config.Type),
		Config:  rawJSON,
	}
}

// seedPresetProtocols 逐条补齐缺失的预置协议：预置 ID 不在库中才写入——
// 幂等；库中优先，升级不覆盖用户对已有预置的编辑；不触碰自定义协议。
// 老库（已有自定义协议）升级后同样能拿到缺失的默认协议。
// legacyPresetHashes 记录各预置「上一版内容」的规范化哈希
// （json.Marshal(config) 后 sha256）：升级判断「行未被用户改动」的依据。
// 每次发布新预置版本时，把上一版哈希登记进此表。
var legacyPresetHashes = map[string]string{
	"chat-completions-api": "86f959ef404dc7a9bf543851c9e14146a1e279ef3c3b01ade375bff0598eb415",
	"anthropic-api":        "006284c9d72573d340434ac2378ff501bd506cac01da3cfaa0a3a60594bbc7d2",
	"gemini-api":           "832c2a3ba8f9e21c65666426849a908a2c7e0762d9267a6fa59b3928ad1d433f",
	"responses-api":        "cdbe42c33f03c0be4d4d8d69c4d2ec40ab5071a9577ff36d3e86b5cdf20dec75",
}

func (s *Server) seedPresetProtocols() {
	if s.store == nil {
		return
	}
	configs, err := PresetProtocolConfigs()
	if err != nil {
		log.Printf("custom protocol presets unavailable: %v", err)
		return
	}
	existing, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		log.Printf("custom protocol preset seeding aborted: %v", err)
		return
	}
	rows := make(map[string]string, len(existing))
	for _, row := range existing {
		rows[row.ID] = row.Config
	}
	seeded, upgraded := 0, 0
	for _, config := range configs {
		stored, known := rows[config.ID]
		if known {
			// 已存在：仅当行内容仍是旧版预置原文（未被用户改动）时自动升级；
			// 用户改过的预置不覆盖——删除后重启即可重新获得新版。
			if legacy := legacyPresetHashes[config.ID]; legacy != "" && presetContentHash(stored) == legacy {
				encoded, err := json.Marshal(config)
				if err == nil {
					if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(config, string(encoded))); err == nil {
						upgraded++
						continue
					}
					log.Printf("custom protocol preset upgrade failed for %q: %v", config.ID, err)
				}
			}
			continue
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			log.Printf("custom protocol preset seed skipped %q: marshal: %v", config.ID, err)
			continue
		}
		if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(config, string(encoded))); err != nil {
			log.Printf("custom protocol preset seed failed for %q: %v", config.ID, err)
			continue
		}
		seeded++
	}
	if seeded > 0 {
		log.Printf("seeded %d missing preset protocol(s) into the database", seeded)
	}
	if upgraded > 0 {
		log.Printf("upgraded %d unmodified preset protocol(s) to the latest version", upgraded)
	}
}

// presetContentHash 计算存储行内容的规范化哈希：存储值本就是
// json.Marshal(config) 的产物，直接对行文本取 sha256 与登记哈希可比。
func presetContentHash(stored string) string {
	sum := sha256.Sum256([]byte(stored))
	return hex.EncodeToString(sum[:])
}

// presetProtocolRenames 是预置协议去厂商化的历史 ID 迁移表；新增预置改名时
// 在此登记一对即可（幂等）。
var presetProtocolRenames = []storage.ProtocolRenamePair{
	{OldID: "openai-chat", NewID: "chat-completions-api"},
	{OldID: "openai-responses", NewID: "responses-api"},
	{OldID: "anthropic-messages", NewID: "anthropic-api"},
	{OldID: "gemini-generate", NewID: "gemini-api"},
}

// migratePresetProtocolRenames 执行预置 ID 改名并同步重写 custom:<id> 平台
// 引用，须在 seedPresetProtocols 之前调用（老库先改名，补齐逻辑再填新装库）。
func (s *Server) migratePresetProtocolRenames() {
	if s.store == nil {
		return
	}
	if _, err := s.store.MigratePresetProtocolRenames(context.Background(), presetProtocolRenames); err != nil {
		log.Printf("custom protocol preset rename migration failed: %v", err)
	}
}
