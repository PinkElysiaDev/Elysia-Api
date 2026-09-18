package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 预置协议定义随二进制分发（沿 model_catalog 快照先例）；首次启动（协议表
// 为空）时写入数据库，此后作为普通行加载——库中优先、升级不覆盖、完全可
// 编辑，「引擎在代码里，协议在数据里」。
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

// presetProtocolAnthropicMessagesID 标记 AI 助手 few-shot 范例所用预置;
// 改名/删除该预设时此常量是唯一需要同步的位置。
const presetProtocolAnthropicMessagesID = "anthropic-messages"

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

// seedPresetProtocols 在协议表为空（首次启动）时写入预置协议定义。用户此后
// 可自由编辑/删除；仅当全部协议行被清空时，下次启动会重新播种。
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
	if len(existing) > 0 {
		return
	}
	seeded := 0
	for _, config := range configs {
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
		log.Printf("seeded %d preset protocol(s) into the database", seeded)
	}
}
