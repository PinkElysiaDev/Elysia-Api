package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/storage"
)

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (s *Server) refreshAllSources(ctx context.Context) (int, error) {
	if s.store == nil {
		return 0, fmt.Errorf("sqlite store is unavailable")
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		count, err := s.refreshSourceByValue(ctx, source)
		if err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
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
	switch source.Platform {
	case "claude":
		// Claude 平台不支持自动拉取：中转站普遍不实现 Anthropic 原生的模型发现端点。
		// 直接返回明确提示，引导用户在模型源里手动添加模型（manualModels）。
		return nil, fmt.Errorf("Claude 平台不支持自动拉取模型，请在模型源中关闭自动拉取并手动添加模型")
	case "gemini":
		return s.fetchGeminiModels(ctx, source)
	default:
		return s.fetchOpenAIModels(ctx, source)
	}
}

func (s *Server) fetchOpenAIModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("source baseUrl is required")
	}
	endpoint := baseURL + "/models"
	if !strings.Contains(baseURL, "/v1") {
		endpoint = baseURL + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if source.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+source.APIKey)
	}
	var raw map[string]any
	if err := doJSON(req, &raw); err != nil {
		return nil, err
	}
	items := extractOpenAIModelIDs(raw)
	models := make([]storage.Model, 0, len(items))
	for _, item := range items {
		models = append(models, inferredModel(source, item, item))
	}
	return models, nil
}

func (s *Server) fetchGeminiModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	endpoint := baseURL + "/v1beta/models"
	if strings.Contains(baseURL, "/v1beta") {
		endpoint = baseURL + "/models"
	}
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
	if err := doJSON(req, &raw); err != nil {
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

func doJSON(req *http.Request, target any) error {
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
