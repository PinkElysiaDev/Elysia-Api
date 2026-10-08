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

func (s *Server) refreshSourceByID(ctx context.Context, sourceID string) (refreshSummary, error) {
	summary := refreshSummary{Added: []string{}, Removed: []string{}}
	source, found, err := s.findSourceByID(ctx, sourceID)
	if err != nil {
		return summary, err
	}
	if !found {
		return summary, fmt.Errorf("model source %q not found", sourceID)
	}
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

	fetched, err := s.fetchSourceModelsByKey(ctx, source)
	summary.Keys = fetched.outcomes
	if err != nil {
		return summary, err
	}
	if len(fetched.models) == 0 {
		return summary, fmt.Errorf("source %q returned no models; kept existing data", source.Name)
	}
	result, err := s.store.CommitSourceRefresh(ctx, source, fetched.models, fetched.keyModels)
	if err != nil {
		return summary, err
	}
	summary.Count, summary.Added, summary.Removed = len(fetched.models), result.Added, result.Removed
	return summary, nil
}

type keyFetchJob struct {
	fetched []storage.Model
	err     error
}

type modelFetchResult struct {
	models    []storage.Model
	keyModels map[int][]string
	outcomes  []keyFetchOutcome
}

func (s *Server) fetchSourceModelsByKey(ctx context.Context, source storage.ModelSource) (modelFetchResult, error) {
	result := modelFetchResult{keyModels: make(map[int][]string)}
	if len(source.APIKeys) == 0 {
		models, err := s.fetchModelsFromSource(ctx, source, source.APIKey)
		result.models = models
		return result, err
	}
	jobs := make(map[int]*keyFetchJob)
	var wg sync.WaitGroup
	for index, entry := range source.APIKeys {
		if entry.Disabled || strings.TrimSpace(entry.Value) == "" {
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
	if len(jobs) == 0 {
		return result, fmt.Errorf("source %q has no enabled API keys", source.Name)
	}
	seen := make(map[string]bool)
	failed := 0
	for index, entry := range source.APIKeys {
		job, attempted := jobs[index]
		if !attempted {
			continue
		}
		outcome := keyFetchOutcome{Index: index, Note: entry.Note, Count: len(job.fetched)}
		if job.err != nil {
			failed++
			outcome.Error = job.err.Error()
		}
		result.outcomes = append(result.outcomes, outcome)
		ids := make([]string, 0, len(job.fetched))
		for _, model := range job.fetched {
			ids = append(ids, model.ID)
			if !seen[model.ID] {
				seen[model.ID] = true
				result.models = append(result.models, model)
			}
		}
		result.keyModels[index] = ids
	}
	if failed > 0 {
		return result, fmt.Errorf("%d/%d keys failed to fetch models; kept existing data", failed, len(jobs))
	}
	return result, nil
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
