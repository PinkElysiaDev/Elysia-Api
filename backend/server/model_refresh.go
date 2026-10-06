package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/storage"
)

// modelFetchTimeout 是模型列表拉取的单请求超时。
const modelFetchTimeout = 30 * time.Second

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
		// 手动集即权威:用户在源编辑里删除的手动模型随之从表中删除(空集
		// 合法——清空全部手动模型也必须落库生效,不再提前返回)。
		result, err := s.store.SyncManualSourceModels(ctx, source, models)
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
		if err != nil {
			return models, nil, err
		}
		// 多 key 减为单 key 后，残留的 per-key 拉取集会把此后上游新增的模型
		// 挡在组外（装配按 KeyAllowsModel 过滤）。单 key 语义是「不限制」
		// （两字段 nil），拉取成功即清残留（含停用 key——重新启用后下次多
		// key 刷新会重新发现）；失败不清，保留旧值优于清空。
		s.clearStalePerKeyPermissions(ctx, source)
		return models, nil, nil
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

// clearStalePerKeyPermissions 清掉源上残留的 per-key 拉取/勾选集（仅当存在
// 残留时写库，避免每次单 key 刷新都空写）。见 fetchSourceModelsByKey 单 key
// 分支注释。
func (s *Server) clearStalePerKeyPermissions(ctx context.Context, source storage.ModelSource) {
	stale := false
	for index := range source.APIKeys {
		if source.APIKeys[index].FetchedModels != nil || source.APIKeys[index].AllowedModels != nil {
			source.APIKeys[index].FetchedModels = nil
			source.APIKeys[index].AllowedModels = nil
			stale = true
		}
	}
	if !stale {
		return
	}
	if err := s.store.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		_ = s.store.InsertSystemLog(ctx, "warn", "failed to clear stale per-key model permissions", map[string]any{"sourceId": source.ID, "error": err.Error()})
		return
	}
	_ = s.store.InsertSystemLog(ctx, "info", "cleared stale per-key model permissions (source now single-key)", map[string]any{"sourceId": source.ID, "sourceName": source.Name})
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

// sourceFetchBase 解析模型列表拉取用的 base（方向5）：fetch_base_url 显式配置时
// 优先，否则与请求转发的 base_url 一致（现状）。各协议的路径拼接约定不变。
func sourceFetchBase(source storage.ModelSource) string {
	if base := strings.TrimSpace(source.FetchBaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return strings.TrimRight(source.BaseURL, "/")
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
		Platform:     storage.NormalizePlatform(source.Platform),
		Type:         inferModelType(id),
		MaxTokens:    0,
		ThinkingMode: "both",
		Available:    true,
	}
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
