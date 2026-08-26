package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// 内置能力目录快照（scripts/update-model-catalog.mjs 生成，随仓库维护）：
// 网络不可达（models.dev 需代理的环境）时保证零配置开箱即用；运行期在线更新
// 成功后由缓存/内存数据覆盖。与落盘缓存同格式（fetchedAt+body），加载逻辑统一。
//
//go:embed model_catalog_snapshot.json
var modelCatalogSnapshotJSON []byte

// modelCatalog 提供模型能力元数据目录：默认拉取 https://models.dev/api.json，
// 模型刷新入库时按模型 id 匹配目录条目并回填能力字段
// （vision/tools/structured/thinking/maxTokens/type）。
//
// 数据链路（借鉴 axonhub 的三级数据源设计）：
//   远端 JSON → 24h 内存缓存（拉取失败保留上次成功数据）→ 未命中或目录不可达时
//   优雅降级（不改动模型任何字段，保持手动配置）。
//
// URL 与出站代理可在 config.json 的 modelCatalog 段覆盖；代理未配置时走
// 环境变量（http.Transport 默认行为）。
type modelCatalog struct {
	getter func() config.ModelCatalogConfig
	// cachePathGetter 返回落盘缓存文件路径（数据库同目录下 model-catalog.json）；
	// 返回空串表示禁用落盘（如测试场景）。
	cachePathGetter func() string

	mu         sync.RWMutex
	entries    map[string]*catalogEntry // 规范化 key → 条目（含 provider/id 与裸 id 两种键）
	count      int                      // 目录内模型条目数（按 provider/model 计，不含裸 id 重复键）
	lastSync   time.Time
	lastTry    time.Time
	lastError  string
	refreshing bool
	// origin 记录当前数据来源：snapshot（内置快照）/ cache（落盘缓存）/ network
	// （在线更新）/ empty，供状态接口诊断。
	origin string
	// sourceURL 记录最近一次在线更新命中的数据源。
	sourceURL string
}

type catalogEntry struct {
	provider   string
	id         string
	name       string
	vision     bool
	tools      bool
	structured bool
	reasoning  bool
	maxTokens  int
	modelType  string
}

const (
	modelCatalogDefaultURL   = "https://models.dev/api.json"
	modelCatalogRetryBackoff = 10 * time.Minute
	// 目录拉取超时：目录不可达时不能拖慢模型刷新路径（ensureLoaded 已非阻塞，
	// 这里只是后台拉取自身的上限），15s 足够完成正常下载。
	modelCatalogFetchTimeout = 15 * time.Second
	// 定期循环的检查粒度：周期配置可运行时修改，按此间隔动态重算是否到期。
	modelCatalogPeriodicTick = 30 * time.Second
)

// catalogSyncDue 判断按当前配置的同步周期是否到期（动态读取，改配置即时生效）。
// 显式配置 0 = 不启用定期同步：只有 force（管理页「立即更新」）才会拉取，
// 内置快照与本地缓存照常可用。
func (m *modelCatalog) syncIntervalDue() bool {
	cfg := config.ModelCatalogConfig{}
	if m.getter != nil {
		cfg = m.getter()
	}
	interval, enabled := cfg.ModelCatalogSyncInterval()
	if !enabled {
		return false
	}
	m.mu.RLock()
	due := time.Since(m.lastSync) >= interval
	m.mu.RUnlock()
	return due
}

func newModelCatalog(getter func() config.ModelCatalogConfig, cachePathGetter func() string) *modelCatalog {
	catalog := &modelCatalog{getter: getter, cachePathGetter: cachePathGetter, entries: map[string]*catalogEntry{}, origin: "empty"}
	catalog.loadFromSnapshot()
	catalog.loadFromCache()
	return catalog
}

// loadFromSnapshot 恢复内置快照（快照生成时间作为 lastSync，供与缓存比新旧）。
func (m *modelCatalog) loadFromSnapshot() {
	if len(modelCatalogSnapshotJSON) == 0 {
		return
	}
	var snapshot catalogCacheFile
	if err := json.Unmarshal(modelCatalogSnapshotJSON, &snapshot); err != nil || len(snapshot.Body) == 0 {
		return
	}
	dataset, err := parseCatalogDataset(snapshot.Body)
	if err != nil || dataset.count == 0 {
		return
	}
	m.mu.Lock()
	m.entries = dataset.entries
	m.count = dataset.count
	m.lastSync = snapshot.FetchedAt
	m.origin = "snapshot"
	m.mu.Unlock()
}

// catalogCacheFile 是落盘缓存的包装结构：原始目录 JSON + 拉取时间。
type catalogCacheFile struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Body      json.RawMessage `json:"body"`
}

// loadFromCache 启动时从落盘缓存恢复目录数据——重启后无需等网络即可用。
// 与内置快照取 fetchedAt 较新者：防止旧缓存倒灌覆盖新版本二进制自带的更新快照。
func (m *modelCatalog) loadFromCache() {
	if m == nil || m.cachePathGetter == nil {
		return
	}
	path := m.cachePathGetter()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cached catalogCacheFile
	if err := json.Unmarshal(raw, &cached); err != nil || len(cached.Body) == 0 {
		return
	}
	dataset, err := parseCatalogDataset(cached.Body)
	if err != nil || dataset.count == 0 {
		return
	}
	m.mu.Lock()
	if cached.FetchedAt.After(m.lastSync) {
		m.entries = dataset.entries
		m.count = dataset.count
		m.lastSync = cached.FetchedAt
		m.origin = "cache"
	}
	m.mu.Unlock()
}

// saveToCache 把成功的拉取结果原子落盘（临时文件 + 重命名），失败仅记为状态错误
// 不影响内存数据。
func (m *modelCatalog) saveToCache(body []byte, fetchedAt time.Time) {
	if m == nil || m.cachePathGetter == nil {
		return
	}
	path := m.cachePathGetter()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.mu.Lock()
		m.lastError = fmt.Sprintf("cache write: %v", err)
		m.mu.Unlock()
		return
	}
	payload, err := json.Marshal(catalogCacheFile{FetchedAt: fetchedAt, Body: body})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		m.mu.Lock()
		m.lastError = fmt.Sprintf("cache write: %v", err)
		m.mu.Unlock()
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

func modelCatalogEnabled(cfg config.ModelCatalogConfig) bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

// catalogResolveURL 返回生效的目录地址（配置覆盖或默认 models.dev）。
func catalogResolveURL(cfg config.ModelCatalogConfig) string {
	if url := strings.TrimSpace(cfg.URL); url != "" {
		return url
	}
	return modelCatalogDefaultURL
}

// catalogHTTPClient 构造拉取目录用的 HTTP client：显式代理 > 环境变量代理。
func catalogHTTPClient(proxy string) *http.Client {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if p := strings.TrimSpace(proxy); p != "" {
		transport.Proxy = func(*http.Request) (u *url.URL, err error) {
			return url.Parse(p)
		}
	}
	return &http.Client{Timeout: modelCatalogFetchTimeout, Transport: transport}
}

// ensureLoaded 确保目录数据可用：数据过期时**异步**触发后台拉取，立即返回——
// 调用方（模型刷新路径）绝不阻塞等待网络（models.dev 不可达时曾把刷新拖慢 60s）。
// 本次调用用旧数据（首次则空目录，Enrich 全部 no-op），后台拉取成功后下一次
// 刷新即获得能力回填。并发触发只允许一个执行拉取；失败后 10 分钟内不重试。
func (m *modelCatalog) ensureLoaded(ctx context.Context) {
	m.triggerRefreshIfNeeded(false)
}

// runPeriodic 后台定期循环：按 tick 粒度检查目录是否超过配置周期，到期则触发
// 后台更新。周期动态读取配置，管理页修改后无需重启即生效。
func (m *modelCatalog) runPeriodic() {
	if m == nil {
		return
	}
	for {
		time.Sleep(modelCatalogPeriodicTick)
		m.triggerRefreshIfNeeded(false)
	}
}

// triggerRefreshIfNeeded 判定是否需要刷新（force=true 绕过周期与退避）并按需启动
// 后台（异步）拉取。返回是否已启动。
func (m *modelCatalog) triggerRefreshIfNeeded(force bool) bool {
	if m == nil {
		return false
	}
	cfg := config.ModelCatalogConfig{}
	if m.getter != nil {
		cfg = m.getter()
	}
	if !modelCatalogEnabled(cfg) {
		return false
	}
	// 先在锁外计算到期判定（syncIntervalDue 自身取读锁，避免递归加锁）。
	syncDue := m.syncIntervalDue()
	m.mu.RLock()
	shouldFetch := force ||
		(syncDue &&
			time.Since(m.lastTry) >= modelCatalogRetryBackoff &&
			!m.refreshing)
	m.mu.RUnlock()
	if !shouldFetch {
		return false
	}

	m.mu.Lock()
	// 双检：可能已有后台拉取在进行。
	shouldFetch = force ||
		(syncDue &&
			time.Since(m.lastTry) >= modelCatalogRetryBackoff &&
			!m.refreshing)
	if !shouldFetch {
		m.mu.Unlock()
		return false
	}
	m.refreshing = true
	m.mu.Unlock()

	// 后台拉取：独立 context，不跟随请求生命周期（请求结束不应中止目录加载）。
	go func() {
		backgroundCtx, cancel := context.WithTimeout(context.Background(), modelCatalogFetchTimeout+5*time.Second)
		defer cancel()
		m.performRefresh(backgroundCtx, cfg)
	}()
	return true
}

// Refresh 立即同步刷新目录（管理端点用）：绕过周期/退避判定；已有拉取进行中时
// 等待其完成后返回最新状态。返回状态 map 与是否发生了实际拉取。
func (m *modelCatalog) Refresh() (map[string]any, bool) {
	if m == nil {
		return map[string]any{"enabled": false}, false
	}
	cfg := config.ModelCatalogConfig{}
	if m.getter != nil {
		cfg = m.getter()
	}
	if !modelCatalogEnabled(cfg) {
		return map[string]any{"enabled": false, "lastError": "model catalog is disabled"}, false
	}
	m.mu.RLock()
	refreshing := m.refreshing
	m.mu.RUnlock()
	if refreshing {
		// 已在进行（后台或并发的强制刷新）：等待完成（上限 = 拉取超时 + 余量）。
		deadline := time.Now().Add(modelCatalogFetchTimeout + 10*time.Second)
		for {
			m.mu.RLock()
			refreshing = m.refreshing
			m.mu.RUnlock()
			if !refreshing || time.Now().After(deadline) {
				return m.Status(), false
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	m.mu.Lock()
	if m.refreshing { // 双检（等待窗口外的并发）
		m.mu.Unlock()
		return m.Status(), false
	}
	m.refreshing = true
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), modelCatalogFetchTimeout+5*time.Second)
	defer cancel()
	m.performRefresh(ctx, cfg)
	return m.Status(), true
}

// performRefresh 执行一次实际拉取并交换内存数据（调用方已置 refreshing）。
func (m *modelCatalog) performRefresh(ctx context.Context, cfg config.ModelCatalogConfig) {
	dataset, body, sourceURL, err := m.fetch(ctx, cfg)

	m.mu.Lock()
	m.refreshing = false
	m.lastTry = time.Now()
	if err != nil {
		// 失败保留既有 entries（内置快照/缓存继续可用）。
		m.lastError = err.Error()
	} else {
		m.entries = dataset.entries
		m.count = dataset.count
		m.lastSync = time.Now()
		m.lastError = ""
		m.origin = "network"
		m.sourceURL = sourceURL
	}
	m.mu.Unlock()

	if err == nil {
		m.saveToCache(body, time.Now())
	}
}

// catalogDataset 是一次成功拉取的解析结果。
type catalogDataset struct {
	entries map[string]*catalogEntry
	count   int
}

// catalogModelJSON 对应 models.dev 条目模型字段的宽容子集（字段缺失按零值处理）。
type catalogModelJSON struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	ToolCall          *bool  `json:"tool_call"`
	StructuredOutput  *bool  `json:"structured_output"`
	Modalities        *struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Reasoning *struct {
		Supported *bool `json:"supported"`
	} `json:"reasoning"`
	Limit *struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

// catalogSourceURLs 返回在线更新的候选数据源（按序尝试直到成功）：
// 配置的 URL（如有）→ models.dev 官方 → jsDelivr 镜像（国内通常直连可达，
// 实测 cdn/fastly 均可；镜像与官方同数据格式，models 数组形态由解析器兼容）。
func catalogSourceURLs(cfg config.ModelCatalogConfig) []string {
	urls := make([]string, 0, 4)
	if custom := strings.TrimSpace(cfg.URL); custom != "" && custom != modelCatalogDefaultURL {
		urls = append(urls, custom)
	}
	urls = append(urls,
		modelCatalogDefaultURL,
		"https://cdn.jsdelivr.net/gh/ThinkInAIXYZ/PublicProviderConf@dev/dist/all.json",
		"https://fastly.jsdelivr.net/gh/ThinkInAIXYZ/PublicProviderConf@dev/dist/all.json",
	)
	return urls
}

// fetch 依次尝试候选数据源拉取并解析目录，返回原始 body（供落盘缓存）与命中的
// 数据源。全部失败时返回最后一个错误。
func (m *modelCatalog) fetch(ctx context.Context, cfg config.ModelCatalogConfig) (*catalogDataset, []byte, string, error) {
	client := catalogHTTPClient(cfg.Proxy)
	var lastErr error
	for _, sourceURL := range catalogSourceURLs(cfg) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s: %s", sourceURL, resp.Status)
			continue
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		dataset, err := parseCatalogDataset(body)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", sourceURL, err)
			continue
		}
		if dataset.count == 0 {
			lastErr = fmt.Errorf("%s: response contained no model entries", sourceURL)
			continue
		}
		return dataset, body, sourceURL, nil
	}
	if lastErr == nil {
		lastErr = errors.New("model catalog fetch failed: no source configured")
	}
	return nil, nil, "", lastErr
}

// parseCatalogDataset 解析 models.dev api.json。兼容两种 providers 形态：
//   - 对象：{ "openai": { "models": { "gpt-4o": {...} } } }（models.dev api.json）
//   - 数组：{ "openai": { "models": [ { "id": "gpt-4o", ... } ] } }（镜像快照）
// 以及镜像常见的顶层 "providers" 包裹：{ "providers": { ... } }。
func parseCatalogDataset(body []byte) (*catalogDataset, error) {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, fmt.Errorf("model catalog JSON decode failed: %w", err)
	}
	// 镜像包裹：顶层只有 providers 一个有效节点时下钻一层。
	if wrapped, ok := providers["providers"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(wrapped, &inner); err == nil {
			providers = inner
		}
	}
	dataset := &catalogDataset{entries: map[string]*catalogEntry{}}
	slugs := make([]string, 0, len(providers))
	for slug := range providers {
		slugs = append(slugs, slug)
	}
	// 排序保证同名模型跨 provider 冲突时的裸 id 归属是确定性的。
	sort.Strings(slugs)
	for _, slug := range slugs {
		var providerObj struct {
			Models json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(providers[slug], &providerObj); err != nil {
			continue // 跳过无法解析的 provider 节点
		}
		if len(providerObj.Models) == 0 {
			continue
		}
		register := func(raw json.RawMessage, fallbackID string) {
			var model catalogModelJSON
			if err := json.Unmarshal(raw, &model); err != nil {
				return
			}
			if model.ID == "" {
				model.ID = fallbackID
			}
			if model.ID == "" {
				return
			}
			dataset.register(newCatalogEntry(slug, model))
		}
		var asObject map[string]json.RawMessage
		if err := json.Unmarshal(providerObj.Models, &asObject); err == nil {
			for id, raw := range asObject {
				register(raw, id)
			}
			continue
		}
		var asArray []json.RawMessage
		if err := json.Unmarshal(providerObj.Models, &asArray); err == nil {
			for _, raw := range asArray {
				register(raw, "")
			}
		}
	}
	return dataset, nil
}

func newCatalogEntry(provider string, model catalogModelJSON) *catalogEntry {
	entry := &catalogEntry{
		provider: provider,
		id:       model.ID,
		name:     model.Name,
		tools:    model.ToolCall != nil && *model.ToolCall,
		structured: model.StructuredOutput != nil && *model.StructuredOutput,
		reasoning:  model.Reasoning != nil && model.Reasoning.Supported != nil && *model.Reasoning.Supported,
	}
	if model.Modalities != nil {
		for _, modality := range model.Modalities.Input {
			if modality == "image" {
				entry.vision = true
				break
			}
		}
	}
	if model.Limit != nil && model.Limit.Context > 0 {
		entry.maxTokens = model.Limit.Context
	}
	entry.modelType = catalogTypeToStorageType(model.Type)
	return entry
}

// catalogTypeToStorageType 把目录 type 归一到项目内模型 type 枚举。
func catalogTypeToStorageType(catalogType string) string {
	switch strings.ToLower(strings.TrimSpace(catalogType)) {
	case "embedding":
		return "embedding"
	case "rerank", "reranker":
		return "reranker"
	case "", "chat", "llm":
		return "llm"
	default:
		return "llm"
	}
}

// register 把条目登记进索引：provider/id 全键（后者覆盖同名）与裸 id 键（不覆盖，
// 保证确定性）。count 只按全键计。
func (d *catalogDataset) register(entry *catalogEntry) {
	fullKey := catalogKey(entry.provider, entry.id)
	if _, exists := d.entries[fullKey]; !exists {
		d.count++
	}
	d.entries[fullKey] = entry
	bareKey := catalogNormalizeID(entry.id)
	if _, exists := d.entries[bareKey]; !exists {
		d.entries[bareKey] = entry
	}
}

func catalogKey(provider, id string) string {
	return catalogNormalizeID(provider) + "/" + catalogNormalizeID(id)
}

func catalogNormalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

var (
	// 日期后缀：-2024-11-20 / -20241120（Claude、OpenAI 的快照命名）。
	catalogDateDashSuffix   = regexp.MustCompile(`-(20\d{2})-(\d{2})-(\d{2})$`)
	catalogDateCompactSuffix = regexp.MustCompile(`-(20\d{6})$`)
)

// catalogLookupVariants 生成逐步宽松的匹配变体：精确 id → 去 provider 前缀 →
// 去 -latest → 去日期后缀。调用方按序尝试，首个命中即用。
func catalogLookupVariants(id string) []string {
	normalized := catalogNormalizeID(id)
	if normalized == "" {
		return nil
	}
	variants := []string{normalized}
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 && idx+1 < len(normalized) {
		variants = append(variants, normalized[idx+1:])
	}
	if trimmed := strings.TrimSuffix(normalized, "-latest"); trimmed != normalized {
		variants = append(variants, trimmed)
	}
	if loc := catalogDateDashSuffix.FindStringIndex(normalized); loc != nil {
		variants = append(variants, normalized[:loc[0]])
	}
	if loc := catalogDateCompactSuffix.FindStringIndex(normalized); loc != nil {
		variants = append(variants, normalized[:loc[0]])
	}
	return variants
}

// lookup 按模型 id 查目录条目，支持精确/去前缀/去日期后缀的渐进匹配。
func (m *modelCatalog) lookup(modelID string) (*catalogEntry, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, variant := range catalogLookupVariants(modelID) {
		if entry, ok := m.entries[variant]; ok {
			return entry, true
		}
	}
	return nil, false
}

// Enrich 用目录条目回填模型的能力字段，返回是否命中。
// 字段映射：vision←modalities.input 含 image；tools←tool_call；
// structured←structured_output；thinking←reasoning.supported；
// maxTokens←limit.context；type←type（归一）。
// 上游 API 返回的字段（如 Gemini 的 inputTokenLimit）优先级更高：仅当模型当前
// 对应字段为空/零值时才用目录值覆盖，避免用目录猜测顶掉上游权威数据。
func (m *modelCatalog) Enrich(model *storage.Model) bool {
	if m == nil || model == nil {
		return false
	}
	entry, ok := m.lookup(model.ID)
	if !ok {
		return false
	}
	model.VisionCapable = entry.vision
	model.ToolsCapable = entry.tools
	model.StructuredOutput = entry.structured
	if entry.reasoning {
		model.ThinkingMode = "both"
	} else if model.ThinkingMode == "" || model.ThinkingMode == "both" {
		model.ThinkingMode = "non-thinking-only"
	}
	if entry.maxTokens > 0 && model.MaxTokens <= 0 {
		model.MaxTokens = entry.maxTokens
	}
	if entry.modelType != "" && model.Type == "llm" {
		model.Type = entry.modelType
	}
	model.CapabilitySource = "catalog"
	return true
}

// Status 返回目录运行状态，供 GET /api/admin/model-catalog/status。
func (m *modelCatalog) Status() map[string]any {
	if m == nil {
		return map[string]any{"enabled": false}
	}
	cfg := config.ModelCatalogConfig{}
	if m.getter != nil {
		cfg = m.getter()
	}
	enabled := modelCatalogEnabled(cfg)
	interval, syncEnabled := cfg.ModelCatalogSyncInterval()
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := map[string]any{
		"enabled":             enabled,
		"url":                 catalogResolveURL(cfg),
		"entries":             m.count,
		"syncIntervalMinutes": int(interval.Minutes()),
		// syncEnabled=false 表示显式配置 0（不启用定期同步），前端区分展示。
		"periodicSyncEnabled": syncEnabled,
		"source":              m.origin,
		"sourceURL":           m.sourceURL,
		"lastSync":            nil,
	}
	if !m.lastSync.IsZero() {
		status["lastSync"] = m.lastSync.UTC().Format(time.RFC3339)
	}
	if m.lastError != "" {
		status["lastError"] = m.lastError
	}
	return status
}
