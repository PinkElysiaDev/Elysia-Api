package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
		models, err := s.fetchClaudeModels(ctx, source)
		if err != nil {
			log.Printf("claude model fetch failed for source %q, using built-in fallback: %v", source.Name, err)
			return builtInClaudeModels(source), nil
		}
		return models, nil
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

func (s *Server) fetchClaudeModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	endpoint := strings.TrimRight(source.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if source.APIKey != "" {
		req.Header.Set("x-api-key", source.APIKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	var raw map[string]any
	if err := doJSON(req, &raw); err != nil {
		return nil, err
	}
	data, _ := raw["data"].([]any)
	if len(data) == 0 {
		return nil, fmt.Errorf("claude models API returned empty list")
	}
	models := make([]storage.Model, 0, len(data))
	for _, value := range data {
		item, _ := value.(map[string]any)
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		name, _ := item["display_name"].(string)
		if strings.TrimSpace(name) == "" {
			name = id
		}
		model := inferredModel(source, id, name)
		model.Platform = "claude"
		model.Type = "llm"
		model.MaxTokens = 200000
		model.VisionCapable = true
		model.ToolsCapable = true
		model.StructuredOutput = true
		models = append(models, model)
	}
	return models, nil
}

func (s *Server) fetchGeminiModels(ctx context.Context, source storage.ModelSource) ([]storage.Model, error) {
	baseURL := strings.TrimRight(source.BaseURL, "/")
	endpoint := baseURL + "/v1beta/models?key=" + url.QueryEscape(source.APIKey)
	if strings.Contains(baseURL, "/v1beta") {
		endpoint = baseURL + "/models?key=" + url.QueryEscape(source.APIKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := doJSON(req, &raw); err != nil {
		return nil, err
	}
	data, _ := raw["models"].([]any)
	models := make([]storage.Model, 0, len(data))
	for _, value := range data {
		item, _ := value.(map[string]any)
		methods, _ := item["supportedGenerationMethods"].([]any)
		if !containsString(methods, "generateContent") {
			continue
		}
		rawName, _ := item["name"].(string)
		if rawName == "" {
			continue
		}
		name := strings.TrimPrefix(rawName, "models/")
		model := inferredModel(source, rawName, name)
		model.Platform = "gemini"
		model.Type = "llm"
		model.MaxTokens = intFromAny(item["outputTokenLimit"], intFromAny(item["inputTokenLimit"], 128000))
		model.VisionCapable = true
		model.ToolsCapable = true
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
	return storage.Model{
		ID:               id,
		Name:             name,
		Platform:         normalizeSourcePlatform(source.Platform),
		Type:             inferModelType(id),
		MaxTokens:        inferMaxTokens(id),
		VisionCapable:    hasVisionCapability(id),
		ToolsCapable:     hasToolsCapability(id),
		StructuredOutput: hasStructuredOutput(id),
		ThinkingMode:     "both",
		Available:        true,
	}
}

func normalizeSourcePlatform(platform string) string {
	if platform == "openai-compatible" {
		return "openai"
	}
	return platform
}

func builtInClaudeModels(source storage.ModelSource) []storage.Model {
	ids := []string{
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
	}
	models := make([]storage.Model, 0, len(ids))
	for _, id := range ids {
		model := inferredModel(source, id, id)
		model.Platform = "claude"
		model.Type = "llm"
		model.MaxTokens = 200000
		model.VisionCapable = true
		model.ToolsCapable = true
		model.StructuredOutput = true
		models = append(models, model)
	}
	return models
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

func inferMaxTokens(modelID string) int {
	id := strings.ToLower(modelID)
	limits := []struct {
		key   string
		limit int
	}{
		{"gpt-4o-mini", 128000},
		{"gpt-4o", 128000},
		{"gpt-4-turbo", 128000},
		{"gpt-4", 8192},
		{"gpt-3.5-turbo", 16385},
		{"text-embedding-3-small", 8191},
		{"text-embedding-3-large", 8191},
		{"text-embedding-ada-002", 8191},
	}
	for _, item := range limits {
		if strings.Contains(id, item.key) {
			return item.limit
		}
	}
	if strings.Contains(id, "claude") {
		return 200000
	}
	return 128000
}

func hasVisionCapability(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "vision") || strings.Contains(id, "gpt-4o") || strings.Contains(id, "gpt-4-turbo") || strings.Contains(id, "gemini") || strings.Contains(id, "claude-3")
}

func hasToolsCapability(modelID string) bool {
	id := strings.ToLower(modelID)
	return !strings.Contains(id, "gpt-3.5") && !strings.Contains(id, "embedding")
}

func hasStructuredOutput(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "gpt-4o") || strings.Contains(id, "gpt-4-turbo") || strings.Contains(id, "claude")
}

func containsString(values []any, expected string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == expected {
			return true
		}
	}
	return false
}

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
