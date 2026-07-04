package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// sourceRefreshError 记录单个源刷新失败的信息，供全量刷新汇总返回。
type sourceRefreshError struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Error      string `json:"error"`
}

// refreshAllSources 刷新所有启用的源。**单源失败不再中断整体**：收集每个源的
// 错误继续往下，返回累计成功数与各源错误列表。
func (s *Server) refreshAllSources(ctx context.Context) (int, []sourceRefreshError, error) {
	if s.store == nil {
		return 0, nil, fmt.Errorf("sqlite store is unavailable")
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return 0, nil, err
	}
	total := 0
	var failures []sourceRefreshError
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		count, err := s.refreshSourceByValue(ctx, source)
		if err != nil {
			failures = append(failures, sourceRefreshError{SourceID: source.ID, SourceName: source.Name, Error: err.Error()})
			_ = s.store.InsertSystemLog(ctx, "warn", "model source refresh failed", map[string]any{"sourceId": source.ID, "sourceName": source.Name, "error": err.Error()})
			continue
		}
		total += count
	}
	return total, failures, nil
}

func (s *Server) refreshSource(ctx context.Context, id string) (int, error) {
	if s.store == nil {
		return 0, fmt.Errorf("sqlite store is unavailable")
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return 0, err
	}
	for _, source := range sources {
		if source.ID == id {
			return s.refreshSourceByValue(ctx, source)
		}
	}
	return 0, fmt.Errorf("model source %q not found", id)
}

func (s *Server) refreshSourceByValue(ctx context.Context, source storage.ModelSource) (int, error) {
	models := make([]storage.Model, 0)
	if !source.AutoFetchModels {
		for _, model := range source.ManualModels {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			if model.Name == "" {
				model.Name = model.ID
			}
			models = append(models, model)
		}
		return len(models), s.store.ReplaceSourceModels(ctx, source, models)
	}

	fetched, err := s.fetchModelsFromSource(ctx, source)
	if err != nil {
		return 0, err
	}
	return len(fetched), s.store.ReplaceSourceModels(ctx, source, fetched)
}

func (s *Server) fetchModelsFromSource(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	// 按归一化后的 apiFormat 分发，兼容旧的 platform 值（claude/openai/openai-compatible…）。
	switch relay.NormalizeAPIFormat(source.Platform) {
	case relay.APIFormatAnthropic:
		// Anthropic 官方 /v1/models 存在（需 x-api-key + anthropic-version），中转站则
		// 普遍提供 OpenAI 兼容的 /v1/models。两种鉴权都试一遍，返回 OpenAI 风格 {data:[{id}]}。
		return s.fetchClaudeModels(ctx, source)
	case relay.APIFormatGemini:
		return s.fetchGeminiModels(ctx, source)
	default:
		// responses / chat_completions 都用 OpenAI 风格 /v1/models 拉取。
		return s.fetchOpenAIModels(ctx, source)
	}
}

// openAIModelsEndpoint 用用户配置的 base URL 直接拼接 /models。
func openAIModelsEndpoint(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/models"
}

func (s *Server) fetchOpenAIModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("source baseUrl is required")
	}
	endpoint := openAIModelsEndpoint(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if source.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+source.APIKey)
	}
	var raw map[string]any
	if err := fetchAndDecodeJSON(req, &raw); err != nil {
		return nil, err
	}
	items := extractOpenAIModelIDs(raw)
	models := make([]storage.Model, 0, len(items))
	for _, item := range items {
		models = append(models, inferredModel(source, item, item))
	}
	return models, nil
}

// fetchClaudeModels 从 Claude 源拉取模型列表。Anthropic 官方与多数中转站都在
// {baseURL}/v1/models 暴露 OpenAI 风格的列表，差异仅在鉴权头：官方要
// x-api-key + anthropic-version，中转站常用 Authorization: Bearer。
// 这里先试 x-api-key，失败再回退 Bearer，最大化兼容。
func (s *Server) fetchClaudeModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("source baseUrl is required")
	}
	// Claude 的 baseUrl 不含 /v1（relay 适配器自行拼 /v1/messages），故此处补 /v1/models，
	// 与 Anthropic 官方及多数中转站暴露 OpenAI 风格列表的路径一致。不能用
	// openAIModelsEndpoint（它假定 baseUrl 已含 /v1，仅补 /models，属 OpenAI 约定）。
	endpoint := baseURL + "/v1/models"

	attempts := []func(*http.Request){
		func(r *http.Request) {
			if source.APIKey != "" {
				r.Header.Set("x-api-key", source.APIKey)
				r.Header.Set("anthropic-version", "2023-06-01")
			}
		},
		func(r *http.Request) {
			if source.APIKey != "" {
				r.Header.Set("Authorization", "Bearer "+source.APIKey)
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
			models = append(models, m)
		}
		return models, nil
	}
	return nil, fmt.Errorf("claude 模型拉取失败（已尝试 x-api-key 与 Bearer 两种鉴权）: %w", lastErr)
}

func (s *Server) fetchGeminiModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	// Gemini 的 baseUrl 不含 /v1beta（relay 适配器自行拼 /v1beta/models/{model}:...），
	// 故此处补 /v1beta/models，与 Google 官方列表端点及上方 tokenLimit 解析注释一致。
	endpoint := baseURL + "/v1beta/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// 用 x-goog-api-key header 传 key（对照 new-api）。中转站多只识别 header 形式，
	// 旧实现用 ?key= query param 在中转站（如 moyuu.cc）会被拒。
	if source.APIKey != "" {
		req.Header.Set("x-goog-api-key", source.APIKey)
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
