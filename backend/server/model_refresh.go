package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// refreshSummary 汇总一次源刷新的结果：模型总数、新增/移除清单与逐 key 拉取结果
// （多 key 源的权限发现，供前端按 key 展示与 toast 提示）。
type refreshSummary struct {
	Count   int               `json:"count"`
	Added   []string          `json:"added"`
	Removed []string          `json:"removed"`
	Keys    []keyFetchOutcome `json:"keys,omitempty"`
}

// keyFetchOutcome 记录多 key 源中单个 key 的拉取结果。Index 对应该 key 在
// 源 key 列表（含停用项）中的下标，前端按行号对应。
type keyFetchOutcome struct {
	Index int    `json:"index"`
	Note  string `json:"note,omitempty"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

func (s *Server) refreshSourceByValue(ctx context.Context, source storage.ModelSource) (refreshSummary, error) {
	empty := refreshSummary{Added: []string{}, Removed: []string{}}
	models := make([]storage.Model, 0)
	if !source.AutoFetchModels {
		for _, model := range source.ManualModels {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			if model.Name == "" {
				model.Name = model.ID
			}
			// 手动模型走 manual 来源：刷新合并永不触碰/删除（用户数据不随上游变动丢失）。
			// 能力字段不做目录自动回填——手动模型是用户显式配置，布尔零值无法区分
			// 「未设置」与「显式 false」，自动覆盖会破坏用户意图（UI 提供单独的目录填充入口）。
			model.Origin = "manual"
			model.Enabled = true
			models = append(models, model)
		}
		if len(models) == 0 {
			return empty, nil
		}
		result, err := s.store.MergeSourceModels(ctx, source, models)
		return refreshSummary{Count: len(models), Added: result.Added, Removed: result.Removed}, err
	}

	fetched, summaryKeys, err := s.fetchSourceModelsByKey(ctx, source)
	if err != nil {
		return empty, err
	}
	if len(fetched) == 0 {
		// 上游 200 但解析不出任何模型（中转站返回 {"error":...} 等异常结构是常态）。
		// 合并会移除上游消失的 fetched 行，空列表意味着清空该源全部拉取模型、
		// 相关模型组变成"无可用模型"——跳过合并并告警。
		_ = s.store.InsertSystemLog(ctx, "warn", "model source refresh returned no models; kept existing models", map[string]any{"sourceId": source.ID, "sourceName": source.Name})
		return refreshSummary{Added: []string{}, Removed: []string{}, Keys: summaryKeys}, fmt.Errorf("source %q returned no models; kept existing model list", source.Name)
	}
	result, err := s.store.MergeSourceModels(ctx, source, fetched)
	return refreshSummary{Count: len(fetched), Added: result.Added, Removed: result.Removed, Keys: summaryKeys}, err
}

// 逐 key 并行拉取（每个 key 一个 goroutine）：key 间互不依赖，中转站 /models
// 响应慢时总耗时 ≈ 最慢一个 key，而非全部之和。结果按下标回填保持顺序稳定。
type keyFetchJob struct {
	fetched []storage.Model
	err     error
}

func (s *Server) fetchPerKey(ctx context.Context, source storage.ModelSource) map[int]*keyFetchJob {
	jobs := make(map[int]*keyFetchJob)
	var wg sync.WaitGroup
	for index := range source.APIKeys {
		entry := source.APIKeys[index]
		if entry.Disabled || entry.Value == "" {
			continue
		}
		job := &keyFetchJob{}
		jobs[index] = job
		wg.Add(1)
		go func() {
			defer wg.Done()
			job.fetched, job.err = s.fetchModelsFromSource(ctx, source, entry.Value)
		}()
	}
	wg.Wait()
	return jobs
}

// fetchSourceModelsByKey 解析拉取用的 key 并执行拉取：
//   - 多 key（启用数 >1）：逐 key 独立拉取（并行），返回模型并集与逐 key 结果；
//     成功 key 的 fetchedModels/allowedModels 回写进 source.APIKeys（调用方负责
//     持久化）——这是「key 分组权限自动发现」：每个 key 拉到的集合即其可用模型集；
//   - 单 key：原单次拉取，不写 per-key 权限字段（行为与历史版本一致）。
func (s *Server) fetchSourceModelsByKey(ctx context.Context, source storage.ModelSource) ([]storage.Model, []keyFetchOutcome, error) {
	effective := source.EffectiveKeys()
	if len(effective) <= 1 {
		key := source.APIKey
		if len(effective) == 1 {
			key = effective[0].Value
		}
		models, err := s.fetchModelsFromSource(ctx, source, key)
		return models, nil, err
	}

	jobs := s.fetchPerKey(ctx, source)
	outcomes := make([]keyFetchOutcome, 0, len(jobs))
	union := make([]storage.Model, 0)
	seen := make(map[string]struct{})
	anySuccess := false
	var lastErr error
	for index := range source.APIKeys {
		entry := source.APIKeys[index]
		job, attempted := jobs[index]
		if !attempted {
			continue // 停用/空 key 不参与
		}
		outcome := keyFetchOutcome{Index: index, Note: entry.Note}
		if job.err != nil {
			lastErr = job.err
			outcome.Error = job.err.Error()
			outcomes = append(outcomes, outcome)
			// 失败 key 保留旧的权限字段（可能过期但优于清空），继续其余 key。
			continue
		}
		fetched := job.fetched
		anySuccess = true
		outcome.Count = len(fetched)
		outcomes = append(outcomes, outcome)
		for _, model := range fetched {
			if _, dup := seen[model.ID]; dup {
				continue
			}
			seen[model.ID] = struct{}{}
			union = append(union, model)
		}
		ids := make([]string, 0, len(fetched))
		for _, model := range fetched {
			ids = append(ids, model.ID)
		}
		// 回写权限字段：fetchedModels = 该 key 拉到的集合；allowedModels 重置为
		// nil（新拉取默认全启用，用户在勾选界面裁剪后才产生非 nil 子集）。
		source.APIKeys[index].FetchedModels = ids
		source.APIKeys[index].AllowedModels = nil
	}
	if !anySuccess {
		return nil, outcomes, fmt.Errorf("all %d keys failed to fetch models; last error: %w", len(effective), lastErr)
	}
	// 逐 key 结果持久化（失败 key 保持旧值不影响整体成功路径）。
	if err := s.store.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		return nil, outcomes, fmt.Errorf("persist per-key model permissions: %w", err)
	}
	return union, outcomes, nil
}

// enrichModelFromCatalog 用能力目录（models.dev）回填模型能力字段（方向1）。
// 目录不可用/未命中时 no-op；上游 API 已返回的字段（如 Gemini inputTokenLimit）
// 优先于目录值（Enrich 内部按「仅空值覆盖」处理）。
func (s *Server) enrichModelFromCatalog(model *storage.Model) {
	if s.catalog == nil || model == nil {
		return
	}
	s.catalog.ensureLoaded(context.Background())
	s.catalog.Enrich(model)
}

// fetchModelsFromSource 按源协议分发拉取。apiKey 显式传入（多 key 源逐 key 调用），
// 兼容旧的 platform 值（claude/openai/openai-compatible…）。
func (s *Server) fetchModelsFromSource(ctx context.Context, source storage.ModelSource, apiKey string) ([]storage.Model, error) {
	apiFormat := relay.NormalizeAPIFormat(source.Platform)
	if strings.HasPrefix(apiFormat, "custom:") {
		return nil, fmt.Errorf("custom protocol source %q does not define model discovery; disable autoFetchModels and configure manual models", source.Platform)
	}
	switch apiFormat {
	case relay.APIFormatAnthropic:
		// Anthropic 官方 /v1/models 存在（需 x-api-key + anthropic-version），中转站则
		// 普遍提供 OpenAI 兼容的 /v1/models。两种鉴权都试一遍，返回 OpenAI 风格 {data:[{id}]}。
		return s.fetchClaudeModels(ctx, source, apiKey)
	case relay.APIFormatGemini:
		return s.fetchGeminiModels(ctx, source, apiKey)
	default:
		// responses / chat_completions 都用 OpenAI 风格 /v1/models 拉取。
		return s.fetchOpenAIModels(ctx, source, apiKey)
	}
}

// sourceFetchBase 解析模型列表拉取用的 base（方向5）：fetch_base_url 显式配置时
// 优先，否则与请求转发的 base_url 一致（现状）。各协议的路径拼接约定不变。
func sourceFetchBase(source storage.ModelSource) string {
	if base := strings.TrimSpace(source.FetchBaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return strings.TrimRight(source.BaseURL, "/")
}

// openAIModelsEndpoint 用用户配置的 base URL 直接拼接 /models。
func openAIModelsEndpoint(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/models"
}

func (s *Server) fetchOpenAIModels(ctx context.Context, source storage.ModelSource, apiKey string) ([]storage.Model, error) {
	baseURL := sourceFetchBase(source)
	if baseURL == "" {
		return nil, fmt.Errorf("source baseUrl is required")
	}
	endpoint := openAIModelsEndpoint(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	var raw map[string]any
	if err := fetchAndDecodeJSON(req, &raw); err != nil {
		return nil, err
	}
	items := extractOpenAIModelIDs(raw)
	models := make([]storage.Model, 0, len(items))
	for _, item := range items {
		model := inferredModel(source, item, item)
		s.enrichModelFromCatalog(&model)
		models = append(models, model)
	}
	return models, nil
}

// fetchClaudeModels 从 Claude 源拉取模型列表。Anthropic 官方与多数中转站都在
// {baseURL}/v1/models 暴露 OpenAI 风格的列表，差异仅在鉴权头：官方要
// x-api-key + anthropic-version，中转站常用 Authorization: Bearer。
// 这里先试 x-api-key，失败再回退 Bearer，最大化兼容。
func (s *Server) fetchClaudeModels(ctx context.Context, source storage.ModelSource, apiKey string) ([]storage.Model, error) {
	baseURL := sourceFetchBase(source)
	if baseURL == "" {
		return nil, fmt.Errorf("source baseUrl is required")
	}
	// Claude 的 baseUrl 不含 /v1（relay 适配器自行拼 /v1/messages），故此处补 /v1/models，
	// 与 Anthropic 官方及多数中转站暴露 OpenAI 风格列表的路径一致。不能用
	// openAIModelsEndpoint（它假定 baseUrl 已含 /v1，仅补 /models，属 OpenAI 约定）。
	endpoint := baseURL + "/v1/models"

	attempts := []func(*http.Request){
		func(r *http.Request) {
			if apiKey != "" {
				r.Header.Set("x-api-key", apiKey)
				r.Header.Set("anthropic-version", "2023-06-01")
			}
		},
		func(r *http.Request) {
			if apiKey != "" {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			}
		},
	}

	var lastErr error
	for _, applyAuth := range attempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		applyAuth(req)
		var raw map[string]any
		if err := fetchAndDecodeJSON(req, &raw); err != nil {
			lastErr = err
			continue
		}
		items := extractOpenAIModelIDs(raw)
		models := make([]storage.Model, 0, len(items))
		for _, item := range items {
			m := inferredModel(source, item, item)
			m.Platform = "claude"
			s.enrichModelFromCatalog(&m)
			models = append(models, m)
		}
		return models, nil
	}
	return nil, fmt.Errorf("claude 模型拉取失败（已尝试 x-api-key 与 Bearer 两种鉴权）: %w", lastErr)
}

func (s *Server) fetchGeminiModels(ctx context.Context, source storage.ModelSource, apiKey string) ([]storage.Model, error) {
	baseURL := sourceFetchBase(source)
	// Gemini 的 baseUrl 不含 /v1beta（relay 适配器自行拼 /v1beta/models/{model}:...），
	// 故此处补 /v1beta/models，与 Google 官方列表端点及上方 tokenLimit 解析注释一致。
	endpoint := baseURL + "/v1beta/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// 用 x-goog-api-key header 传 key（对照 new-api）。中转站多只识别 header 形式，
	// 旧实现用 ?key= query param 在中转站（如 moyuu.cc）会被拒。
	if apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}
	var raw map[string]any
	if err := fetchAndDecodeJSON(req, &raw); err != nil {
		return nil, err
	}
	data, _ := raw["models"].([]any)
	models := make([]storage.Model, 0, len(data))
	for _, value := range data {
		item, _ := value.(map[string]any)
		rawName, _ := item["name"].(string)
		if rawName == "" {
			continue
		}
		// 不再用 supportedGenerationMethods 过滤（对照 new-api）：中转站常不返回该字段，
		// 过滤会漏掉可用模型。仅剥离 models/ 前缀。
		name := strings.TrimPrefix(rawName, "models/")
		model := inferredModel(source, rawName, name)
		model.Platform = "gemini"
		model.Type = "llm"
		// Gemini /v1beta/models 会返回 inputTokenLimit/outputTokenLimit，
		// 返回了就解析（优先 input 作为上下文窗口），没返回则留空（=0）由用户填。
		if limit := intFromAny(item["inputTokenLimit"], 0); limit > 0 {
			model.MaxTokens = limit
		} else if limit := intFromAny(item["outputTokenLimit"], 0); limit > 0 {
			model.MaxTokens = limit
		}
		// 上游未返回 limit 时才用目录值兜底（Enrich 内部按空值覆盖）。
		s.enrichModelFromCatalog(&model)
		models = append(models, model)
	}
	return models, nil
}

func fetchAndDecodeJSON(req *http.Request, target any) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("model fetch failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func extractOpenAIModelIDs(raw map[string]any) []string {
	result := []string{}
	data, _ := raw["data"].([]any)
	for _, value := range data {
		item, _ := value.(map[string]any)
		id, _ := item["id"].(string)
		if id != "" {
			result = append(result, id)
		}
	}
	return result
}

func inferredModel(source storage.ModelSource, id, name string) storage.Model {
	if strings.TrimSpace(name) == "" {
		name = id
	}
	// 原则：API 返回了字段就解析（见各 fetch 函数对 maxTokens 等的赋值），
	// 没返回的才留空，由用户在模型缓存里手动填写——不再硬编码猜测（旧实现一律
	// 给 128000 会对 1M 上下文等新模型造成明显错误）。此处只设可靠的默认：
	// type 轻量推断（embedding/reranker/llm），MaxTokens/能力默认留空。
	return storage.Model{
		ID:           id,
		Name:         name,
		Platform:     normalizeSourcePlatform(source.Platform),
		Type:         inferModelType(id),
		MaxTokens:    0,
		ThinkingMode: "both",
		Available:    true,
	}
}

func normalizeSourcePlatform(platform string) string {
	if platform == "openai-compatible" {
		return "openai"
	}
	return platform
}

func inferModelType(modelID string) string {
	id := strings.ToLower(modelID)
	if strings.Contains(id, "embed") || strings.Contains(id, "text-embedding") {
		return "embedding"
	}
	if strings.Contains(id, "rerank") {
		return "reranker"
	}
	return "llm"
}

// intFromAny 从 JSON 解析出的 any 值中安全提取正整数，无法识别或非正时返回 fallback。
// 用于解析上游返回的 inputTokenLimit/outputTokenLimit 等数值字段。
func intFromAny(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return fallback
}
