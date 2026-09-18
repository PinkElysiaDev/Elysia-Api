package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"

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
			continue
		}
		if err := s.store.UpsertCustomProtocol(context.Background(), storage.CustomProtocol{
			ID:      config.ID,
			Name:    config.Name,
			Version: config.Version,
			Type:    relay.NormalizeCustomProtocolType(config.Type),
			Config:  string(encoded),
		}); err != nil {
			log.Printf("custom protocol preset seed failed for %q: %v", config.ID, err)
			continue
		}
		seeded++
	}
	if seeded > 0 {
		log.Printf("seeded %d preset protocol(s) into the database", seeded)
	}
}
