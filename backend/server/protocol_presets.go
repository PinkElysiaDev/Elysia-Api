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
// legacyPresetHashes 登记各预置历史版本内容的规范化哈希
// （json.Marshal(config) 后 sha256）：升级判断「行未被用户改动」的依据。
// 命中任一代哈希即升级到当前版；每次发布新预置版本时把上一版哈希追加进
// 此表（链式登记,停在任意旧版的老库都能一步升到最新）。
var legacyPresetHashes = map[string][]string{
	"chat-completions-api": {
		"86f959ef404dc7a9bf543851c9e14146a1e279ef3c3b01ade375bff0598eb415", // v1
		"295485921fa104e0ca507371bfe71dd2587cb92de3ddd9f38bfcae92a111220f", // v2
		"327ca4b2dbc1b9564af48700418d2ef27ad0ef9a7d0602a74312bfed0013a22f", // v3
		"81d0c91f116a803728a04e520b26ea6c5c236f872388ab479f52afa31ddaa8c4", // v4
		"40f2f69655f726203bb212e788137469c82ec33e57d5f3a1ffc2767e142575b6", // v5
	},
	"anthropic-api": {
		"006284c9d72573d340434ac2378ff501bd506cac01da3cfaa0a3a60594bbc7d2", // v1
		"db954c7442472fdd8540b19c423fa6c7eaba2c12d934325345078b54541c889d", // v2
		"26c999684e03ed26e34a39378c923bd29b6793cd6ecc14ae12593264b14a9db7", // v3
		"19c8ec6ff491b3c7471ef3d4b6efe6e6f212c5b9107d4c19ad30ad3ebd17fbe5", // v4
	},
	"gemini-api": {
		"832c2a3ba8f9e21c65666426849a908a2c7e0762d9267a6fa59b3928ad1d433f", // v1
		"9e4687a486228f0e6a3ac6cc37db561d014e147dd11025693ffe50821aa306a6", // v2
		"17649cc0f6619488acfa2cfeedacdf94847b3795e4ea1181a33cf9a4de58ba80", // v3
		"86941a1e6b3599d3b5e7a013c752c3f345f202038787774c370cdf6c862f892a", // v4
	},
	"responses-api": {
		"cdbe42c33f03c0be4d4d8d69c4d2ec40ab5071a9577ff36d3e86b5cdf20dec75", // v1
		"166fc489e972d4a07f33deffde41397c4d9072ef6aae57aa3c105a7bd9f62489", // v2
		"8c7a575502904cd0cb773e0ef66a1118aa5076d2a7e58dd05d484f6048b76d24", // v3
		"a72bd5ad968db8a836f9ab38561c2c76872c2eb1ec24b2e001e842c74e5a6b03", // v4
		"54b73998ae8568c4dfef036ed4f0d3b878e6326853429a2dfdfae82db8916015", // v5
	},
}

// seedPresetProtocols 逐条补齐缺失预置并升级未改动的旧版预置,返回本次实际
// 升级到当前版的协议 ID(供 path 语义切换的源 base 迁移按需触发)。
func (s *Server) seedPresetProtocols() (upgradedIDs []string) {
	if s.store == nil {
		return nil
	}
	configs, err := PresetProtocolConfigs()
	if err != nil {
		log.Printf("custom protocol presets unavailable: %v", err)
		return nil
	}
	existing, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		log.Printf("custom protocol preset seeding aborted: %v", err)
		return nil
	}
	rows := make(map[string]string, len(existing))
	for _, row := range existing {
		rows[row.ID] = row.Config
	}
	seeded, upgraded := 0, 0
	upgradedIDs = []string{}
	for _, config := range configs {
		stored, known := rows[config.ID]
		if known {
			// 已存在：仅当行内容仍是历史版本预置原文（未被用户改动）时自动升级；
			// 用户改过的预置不覆盖——删除后重启即可重新获得新版。
			if matchesAnyPresetHash(stored, legacyPresetHashes[config.ID]) {
				encoded, err := json.Marshal(config)
				if err == nil {
					if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(config, string(encoded))); err == nil {
						upgraded++
						upgradedIDs = append(upgradedIDs, config.ID)
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
	return upgradedIDs
}

// presetContentHash 计算存储行内容的规范化哈希：存储值本就是
// json.Marshal(config) 的产物，直接对行文本取 sha256 与登记哈希可比。
func presetContentHash(stored string) string {
	sum := sha256.Sum256([]byte(stored))
	return hex.EncodeToString(sum[:])
}

// matchesAnyPresetHash 判断行内容哈希是否命中登记链中的任一代。
func matchesAnyPresetHash(stored string, legacies []string) bool {
	if len(legacies) == 0 {
		return false
	}
	storedHash := presetContentHash(stored)
	for _, legacy := range legacies {
		if storedHash == legacy {
			return true
		}
	}
	return false
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

// stripGeminiModelIDPrefixes 一次性剥离历史拉取入库的 Gemini 系模型 ID 的
// "models/" 集合前缀（内置 gemini 与 gemini-api 预置的旧拉取 bug 存量修复），
// 须在 syncCustomProtocols 之前调用；幂等。
func (s *Server) stripGeminiModelIDPrefixes() {
	if s.store == nil {
		return
	}
	if _, err := s.store.StripGeminiModelIDPrefixes(context.Background()); err != nil {
		log.Printf("gemini model id prefix migration failed: %v", err)
	}
}

// reconcileCustomProtocolConfigIDs 对账修复「行 id 列与 config 内部 id 脱节」
// 的库（旧版改名迁移只改行 id 列与平台引用，config 内部残留旧 id，注册键因
// 此错位），须在 syncCustomProtocols 之前调用；幂等，一致即无事发生。
func (s *Server) reconcileCustomProtocolConfigIDs() {
	if s.store == nil {
		return
	}
	if _, err := s.store.ReconcileCustomProtocolConfigIDs(context.Background()); err != nil {
		log.Printf("custom protocol config id reconciliation failed: %v", err)
	}
}

// relativePathPresets 登记已把端点路径相对化(request.path 不含版本段、
// base 需含版本段,对齐 OpenAI 官方 base 约定)的预置及其 base 版本段。
// anthropic-api / gemini-api 的路径语义与各自官方约定一致,不在表内。
var relativePathPresets = map[string]string{
	"chat-completions-api": "/v1",
	"responses-api":        "/v1",
}

// migratePresetRelativePathBases 在预置行本次从历史版本升级到 path 相对版
// (v3)时,把这些预置下源 base 补上版本段:旧语义源 base 填站点根,新语义
// 要求含 /v1(否则拼出 /v1/v1/... 或裸端点路径)。仅处理本次发生了行升级
// 的协议——用户改过(未升级)的行语义未变,其源一律不动;此后新建的源按新
// 语义填写,不再自动纠正。幂等:补过的 base 已以版本段结尾,重复调用无事
// 发生。
func (s *Server) migratePresetRelativePathBases(upgraded []string) {
	if s.store == nil || len(upgraded) == 0 {
		return
	}
	upgradedSet := make(map[string]bool, len(upgraded))
	for _, id := range upgraded {
		upgradedSet[id] = true
	}
	for protocolID, suffix := range relativePathPresets {
		if !upgradedSet[protocolID] {
			continue
		}
		platform := "custom:" + protocolID
		updated, err := s.store.AppendSourceBaseURLSuffix(context.Background(), platform, suffix)
		if err != nil {
			log.Printf("[preset-rebase] failed to append %q to sources of %q: %v", suffix, platform, err)
			continue
		}
		if updated > 0 {
			log.Printf("[preset-rebase] appended %q to base of %d source(s) on %q", suffix, updated, platform)
		}
	}
}
