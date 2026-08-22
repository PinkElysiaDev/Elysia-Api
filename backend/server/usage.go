package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type usageBody struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type usageTokenUsage struct {
	InputTokens     *int `json:"inputTokens,omitempty"`
	OutputTokens    *int `json:"outputTokens,omitempty"`
	TotalTokens     *int `json:"totalTokens,omitempty"`
	CacheHitTokens  *int `json:"cacheHitTokens,omitempty"`
	EstimatedTokens int  `json:"estimatedTokens,omitempty"`
	Estimated       bool `json:"estimated,omitempty"`
}

type usageDetail struct {
	InputTokens              *int `json:"inputTokens,omitempty"`
	OutputTokens             *int `json:"outputTokens,omitempty"`
	TotalTokens              *int `json:"totalTokens,omitempty"`
	CachedInputTokens        *int `json:"cachedInputTokens,omitempty"`
	CacheCreationInputTokens *int `json:"cacheCreationInputTokens,omitempty"`
	ReasoningTokens          *int `json:"reasoningTokens,omitempty"`
	TextInputTokens          *int `json:"textInputTokens,omitempty"`
	TextOutputTokens         *int `json:"textOutputTokens,omitempty"`
	ImageInputTokens         *int `json:"imageInputTokens,omitempty"`
	ImageOutputTokens        *int `json:"imageOutputTokens,omitempty"`
	AudioInputTokens         *int `json:"audioInputTokens,omitempty"`
	AudioOutputTokens        *int `json:"audioOutputTokens,omitempty"`
	ToolUseTokens            *int `json:"toolUseTokens,omitempty"`
	Estimated                bool `json:"estimated,omitempty"`
}

type builtinToolUsage struct {
	WebSearchCalls       int `json:"webSearchCalls,omitempty"`
	FileSearchCalls      int `json:"fileSearchCalls,omitempty"`
	ImageGenerationCalls int `json:"imageGenerationCalls,omitempty"`
	CodeInterpreterCalls int `json:"codeInterpreterCalls,omitempty"`
	ComputerUseCalls     int `json:"computerUseCalls,omitempty"`
}

type retryEvent struct {
	Attempt int    `json:"attempt"`
	Model   string `json:"model"`
	Error   string `json:"error,omitempty"`
}

type usageRecord struct {
	RequestID           string           `json:"requestId"`
	StartedAt           time.Time        `json:"startedAt"`
	EndedAt             time.Time        `json:"endedAt"`
	KeyName             string           `json:"keyName"`
	KeyHash             string           `json:"keyHash"`
	RequestedModelGroup string           `json:"requestedModelGroup"`
	GroupID             string           `json:"groupId"`
	GroupName           string           `json:"groupName"`
	ModelID             string           `json:"modelId"`
	ModelName           string           `json:"modelName"`
	Platform            string           `json:"platform"`
	InputFormat         string           `json:"inputFormat"`
	TargetPlatform      string           `json:"targetPlatform"`
	SourceFormat        string           `json:"sourceFormat,omitempty"`
	TargetFormat        string           `json:"targetFormat,omitempty"`
	SourceEndpoint      string           `json:"sourceEndpoint,omitempty"`
	TargetEndpoint      string           `json:"targetEndpoint,omitempty"`
	RelayMode           string           `json:"relayMode,omitempty"`
	ResponsesMode       string           `json:"responsesMode,omitempty"`
	ConversionChain     []string         `json:"conversionChain,omitempty"`
	UsageSource         string           `json:"usageSource,omitempty"`
	RequestWarnings     []string         `json:"requestWarnings,omitempty"`
	Stream              bool             `json:"stream"`
	StatusCode          int              `json:"statusCode"`
	Error               string           `json:"error,omitempty"`
	FirstByteMs         int64            `json:"firstByteMs"`
	DurationMs          int64            `json:"durationMs"`
	Usage               usageTokenUsage  `json:"usage"`
	UsageDetail         usageDetail      `json:"usageDetail,omitempty"`
	BuiltinToolUsage    builtinToolUsage `json:"builtinToolUsage,omitempty"`
	RetryCount          int              `json:"retryCount"`
	RetryEvents         []retryEvent     `json:"retryEvents"`
	IncomingBody        usageBody        `json:"incomingBody"`
	OutgoingBody        usageBody        `json:"outgoingBody"`
	ProviderResponse    usageBody        `json:"providerResponse"`
	DownstreamResponse  usageBody        `json:"downstreamResponse"`

	// downstream 是写回下游客户端的 ResponseWriter 捕获器，运行期内部使用，
	// 不参与 JSON 序列化。recordUsage 会从它回读 DownstreamResponse。
	downstream *downstreamCaptureWriter `json:"-"`
}

type usageSummary struct {
	Requests                     int       `json:"requests"`
	Success                      int       `json:"success"`
	Failed                       int       `json:"failed"`
	SuccessRate                  float64   `json:"successRate"`
	FailedRate                   float64   `json:"failedRate"`
	StreamRequests               int       `json:"streamRequests"`
	EstimatedRequests            int       `json:"estimatedRequests"`
	InputTokens                  int       `json:"inputTokens"`
	OutputTokens                 int       `json:"outputTokens"`
	TotalTokens                  int       `json:"totalTokens"`
	CacheHitTokens               int       `json:"cacheHitTokens"`
	EstimatedTokens              int       `json:"estimatedTokens"`
	ReasoningTokens              int       `json:"reasoningTokens"`
	WebSearchCalls               int       `json:"webSearchCalls"`
	FileSearchCalls              int       `json:"fileSearchCalls"`
	ImageGenerationCalls         int       `json:"imageGenerationCalls"`
	OpenAIChatRequests           int       `json:"openaiChatRequests"`
	ClaudeRequests               int       `json:"claudeRequests"`
	GeminiRequests               int       `json:"geminiRequests"`
	ResponsesRequests            int       `json:"responsesRequests"`
	NativeResponsesRequests      int       `json:"nativeResponsesRequests"`
	TransformedResponsesRequests int       `json:"transformedResponsesRequests"`
	AvgInputTokens               float64   `json:"avgInputTokens"`
	AvgOutputTokens              float64   `json:"avgOutputTokens"`
	AvgTotalTokens               float64   `json:"avgTotalTokens"`
	CacheHitRate                 float64   `json:"cacheHitRate"`
	AvgFirstByteMs               float64   `json:"avgFirstByteMs"`
	P95FirstByteMs               int64     `json:"p95FirstByteMs"`
	AvgDurationMs                float64   `json:"avgDurationMs"`
	P95DurationMs                int64     `json:"p95DurationMs"`
	AvgLatencyMs                 float64   `json:"avgLatencyMs"`
	FirstUsedAt                  time.Time `json:"firstUsedAt,omitempty"`
	FirstModelName               string    `json:"firstModelName,omitempty"`
	LastUsedAt                   time.Time `json:"lastUsedAt,omitempty"`
	LastModelName                string    `json:"lastModelName,omitempty"`
}

type usageAggregate struct {
	Key          string       `json:"key"`
	KeyName      string       `json:"keyName,omitempty"`
	KeyHash      string       `json:"keyHash,omitempty"`
	GroupName    string       `json:"groupName,omitempty"`
	ModelName    string       `json:"modelName,omitempty"`
	Platform     string       `json:"platform,omitempty"`
	Window       string       `json:"window,omitempty"`
	UsageSummary usageSummary `json:"summary"`
}

func shortTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

func (s *Server) initUsageRecord(c *gin.Context, start time.Time, body []byte, inputFormat relay.FormatType) *usageRecord {
	return &usageRecord{
		RequestID:    usageRequestID(start),
		StartedAt:    start,
		KeyName:      c.GetString("elysiaKeyName"),
		KeyHash:      c.GetString("elysiaKeyHash"),
		InputFormat:  string(inputFormat),
		StatusCode:   http.StatusOK,
		IncomingBody: sanitizeUsageBody(body),
	}
}

// usageRequestID 生成带随机后缀的请求 ID：并发请求可能拿到相同的 UnixNano
// （Windows 时钟粒度下概率可观），裸纳秒时间戳会与 INSERT OR REPLACE 相互覆盖。
func usageRequestID(start time.Time) string {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Sprintf("req_%d", start.UnixNano())
	}
	return fmt.Sprintf("req_%d_%x", start.UnixNano(), suffix)
}

func sanitizeUsageBody(data []byte) usageBody {
	truncated := len(data) > UsageBodyMaxBytes
	if truncated {
		data = data[:UsageBodyMaxBytes]
	}

	var value interface{}
	if err := json.Unmarshal(data, &value); err == nil {
		redactJSON(value)
		if sanitized, err := json.Marshal(value); err == nil {
			return usageBody{Content: string(sanitized), Truncated: truncated}
		}
	}

	return usageBody{Content: string(data), Truncated: truncated}
}

func redactJSON(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if isSensitiveUsageKey(key) {
				v[key] = "[REDACTED]"
				continue
			}
			redactJSON(child)
		}
	case []interface{}:
		for _, child := range v {
			redactJSON(child)
		}
	}
}

func isSensitiveUsageKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	switch normalized {
	case "authorization", "apikey", "xapikey", "xgoogapikey", "token", "accesstoken", "key":
		return true
	default:
		return strings.Contains(normalized, "secret") || strings.Contains(normalized, "credential")
	}
}

func intPtr(v int) *int {
	return &v
}

type providerUsageResult struct {
	Usage    usageTokenUsage
	Detail   usageDetail
	Builtin  builtinToolUsage
	Source   string
	HasUsage bool
}

func usageFromProviderBody(platform relay.Platform, body []byte) usageTokenUsage {
	return extractProviderUsageFromBody(platform, "", body).Usage
}

func extractProviderUsageFromBody(platform relay.Platform, format relay.FormatType, body []byte) providerUsageResult {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return providerUsageResult{}
	}
	return extractProviderUsageFromPayload(platform, format, payload, "provider_response")
}

func extractProviderUsageFromStreamEvent(platform relay.Platform, format relay.FormatType, payload string) providerUsageResult {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return providerUsageResult{}
	}
	return extractProviderUsageFromPayload(platform, format, event, "provider_stream")
}

func extractProviderUsageFromPayload(platform relay.Platform, format relay.FormatType, payload map[string]interface{}, source string) providerUsageResult {
	if result := usageFromResponsesStreamPayload(payload, source); result.HasUsage {
		return result
	}
	if raw, ok := payload["usageMetadata"].(map[string]interface{}); ok {
		return usageResultFromGeminiUsageMetadata(raw, source)
	}
	if raw, ok := payload["message"].(map[string]interface{}); ok {
		if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
			return usageResultFromClaudeUsage(usageRaw, source)
		}
	}
	if usageRaw, ok := payload["message_delta"].(map[string]interface{}); ok {
		if raw, ok := usageRaw["usage"].(map[string]interface{}); ok {
			return usageResultFromClaudeUsage(raw, source)
		}
	}
	switch platform {
	case relay.PlatformAnthropic:
		if raw, ok := payload["usage"].(map[string]interface{}); ok {
			return usageResultFromClaudeUsage(raw, source)
		}
		if raw, ok := payload["message"].(map[string]interface{}); ok {
			if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
				return usageResultFromClaudeUsage(usageRaw, source)
			}
		}
		if usageRaw, ok := payload["message_delta"].(map[string]interface{}); ok {
			if raw, ok := usageRaw["usage"].(map[string]interface{}); ok {
				return usageResultFromClaudeUsage(raw, source)
			}
		}
	case relay.PlatformGemini:
		if raw, ok := payload["usageMetadata"].(map[string]interface{}); ok {
			return usageResultFromGeminiUsageMetadata(raw, source)
		}
	default:
		if format == relay.FormatResponses {
			if result := usageResultFromResponsesPayload(payload, source, true); result.HasUsage {
				return result
			}
		}
		result := usageResultFromOpenAICompatiblePayload(payload, source)
		if result.HasUsage {
			return result
		}
	}
	return providerUsageResult{}
}

func applyProviderUsageToRecord(record *usageRecord, result providerUsageResult) {
	if record == nil || !result.HasUsage {
		return
	}
	record.Usage = mergeUsage(record.Usage, result.Usage)
	record.UsageDetail = mergeUsageDetail(record.UsageDetail, result.Detail)
	record.BuiltinToolUsage = mergeBuiltinToolUsage(record.BuiltinToolUsage, result.Builtin)
	if result.Source != "" {
		record.UsageSource = result.Source
	}
}

func usageResultFromOpenAICompatiblePayload(payload map[string]interface{}, source string) providerUsageResult {
	result := providerUsageResult{Source: source}
	if raw, ok := payload["usage"].(map[string]interface{}); ok {
		result = usageResultFromOpenAIUsage(raw, source)
	}
	cacheHitTokens := getInt(result.Usage.CacheHitTokens)
	cacheFieldSeen := result.Usage.CacheHitTokens != nil
	if choices, ok := payload["choices"].([]interface{}); ok {
		for _, choice := range choices {
			choiceMap, ok := choice.(map[string]interface{})
			if !ok {
				continue
			}
			choiceUsage, ok := choiceMap["usage"].(map[string]interface{})
			if !ok {
				continue
			}
			if rawValue, ok := choiceUsage["cached_tokens"]; ok && rawValue != nil {
				cacheFieldSeen = true
				cacheHitTokens = maxInt(cacheHitTokens, int(numberFromUsageMap(choiceUsage, "cached_tokens")))
			}
		}
	}
	if timings, ok := payload["timings"].(map[string]interface{}); ok {
		if rawValue, ok := timings["cache_n"]; ok && rawValue != nil {
			cacheFieldSeen = true
			cacheHitTokens = maxInt(cacheHitTokens, int(numberFromUsageMap(timings, "cache_n")))
		}
	}
	if cacheFieldSeen {
		result.Usage.CacheHitTokens = intPtr(cacheHitTokens)
		result.Detail.CachedInputTokens = intPtr(cacheHitTokens)
		result.HasUsage = true
	}
	if usageHasAnyTokens(result.Usage) {
		result.HasUsage = true
	}
	return result
}

func usageResultFromOpenAIUsage(raw map[string]interface{}, source string) providerUsageResult {
	usage := usageFromOpenAIUsage(raw)
	detail := usageDetail{}
	if usage.InputTokens != nil {
		detail.InputTokens = intPtr(getInt(usage.InputTokens))
	}
	if usage.OutputTokens != nil {
		detail.OutputTokens = intPtr(getInt(usage.OutputTokens))
	}
	if usage.TotalTokens != nil {
		detail.TotalTokens = intPtr(getInt(usage.TotalTokens))
	}
	if usage.CacheHitTokens != nil {
		detail.CachedInputTokens = intPtr(getInt(usage.CacheHitTokens))
	}
	if details, ok := raw["completion_tokens_details"].(map[string]interface{}); ok {
		setDetailInt(&detail.ReasoningTokens, details, "reasoning_tokens")
		setDetailInt(&detail.TextOutputTokens, details, "text_tokens")
		setDetailInt(&detail.AudioOutputTokens, details, "audio_tokens")
		setDetailInt(&detail.ImageOutputTokens, details, "image_tokens")
	}
	if details, ok := raw["output_tokens_details"].(map[string]interface{}); ok {
		setDetailInt(&detail.ReasoningTokens, details, "reasoning_tokens")
		setDetailInt(&detail.TextOutputTokens, details, "text_tokens")
		setDetailInt(&detail.AudioOutputTokens, details, "audio_tokens")
		setDetailInt(&detail.ImageOutputTokens, details, "image_tokens")
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, ok := raw[key].(map[string]interface{}); ok {
			setDetailInt(&detail.TextInputTokens, details, "text_tokens")
			setDetailInt(&detail.AudioInputTokens, details, "audio_tokens")
			setDetailInt(&detail.ImageInputTokens, details, "image_tokens")
		}
	}
	return providerUsageResult{Usage: usage, Detail: detail, Source: source, HasUsage: usageHasAnyTokens(usage)}
}

func usageResultFromResponsesPayload(payload map[string]interface{}, source string, includeOutputTools bool) providerUsageResult {
	result := providerUsageResult{Source: source}
	if raw, ok := payload["usage"].(map[string]interface{}); ok {
		result = usageResultFromOpenAIUsage(raw, source)
	}
	if includeOutputTools {
		result.Builtin = builtinToolUsageFromResponsesOutput(payload["output"])
		if result.Builtin != (builtinToolUsage{}) {
			result.HasUsage = true
		}
	}
	if usageHasAnyTokens(result.Usage) {
		result.HasUsage = true
	}
	return result
}

func usageFromResponsesStreamPayload(payload map[string]interface{}, source string) providerUsageResult {
	if response, ok := payload["response"].(map[string]interface{}); ok {
		if raw, ok := response["usage"].(map[string]interface{}); ok {
			return usageResultFromOpenAIUsage(raw, source)
		}
	}
	if strings.EqualFold(stringValueFromMap(payload, "type"), "response.usage.delta") {
		if raw, ok := payload["usage"].(map[string]interface{}); ok {
			return usageResultFromOpenAIUsage(raw, source)
		}
		if raw, ok := payload["delta"].(map[string]interface{}); ok {
			return usageResultFromOpenAIUsage(raw, source)
		}
	}
	if strings.EqualFold(stringValueFromMap(payload, "type"), "response.output_item.done") {
		if item, ok := payload["item"].(map[string]interface{}); ok {
			builtin := builtinToolUsageFromResponsesItem(item)
			if builtin != (builtinToolUsage{}) {
				return providerUsageResult{Builtin: builtin, Source: source, HasUsage: true}
			}
		}
	}
	return providerUsageResult{}
}

func usageResultFromGeminiUsageMetadata(raw map[string]interface{}, source string) providerUsageResult {
	usage := usageFromGeminiUsageMetadata(raw)
	detail := usageDetail{}
	if usage.InputTokens != nil {
		detail.InputTokens = intPtr(getInt(usage.InputTokens))
	}
	if usage.OutputTokens != nil {
		detail.OutputTokens = intPtr(getInt(usage.OutputTokens))
	}
	if usage.TotalTokens != nil {
		detail.TotalTokens = intPtr(getInt(usage.TotalTokens))
	}
	if usage.CacheHitTokens != nil {
		detail.CachedInputTokens = intPtr(getInt(usage.CacheHitTokens))
	}
	if rawValue, ok := raw["thoughtsTokenCount"]; ok && rawValue != nil {
		detail.ReasoningTokens = intPtr(int(numberFromUsageMap(raw, "thoughtsTokenCount")))
	}
	addGeminiTokenDetails(raw, "promptTokensDetails", &detail.TextInputTokens, &detail.ImageInputTokens, &detail.AudioInputTokens)
	addGeminiTokenDetails(raw, "toolUsePromptTokensDetails", &detail.TextInputTokens, &detail.ImageInputTokens, &detail.AudioInputTokens)
	addGeminiTokenDetails(raw, "candidatesTokensDetails", &detail.TextOutputTokens, &detail.ImageOutputTokens, &detail.AudioOutputTokens)
	return providerUsageResult{Usage: usage, Detail: detail, Source: source, HasUsage: usageHasAnyTokens(usage)}
}

func usageResultFromClaudeUsage(raw map[string]interface{}, source string) providerUsageResult {
	usage := usageFromClaudeUsage(raw)
	detail := usageDetail{}
	if usage.InputTokens != nil {
		detail.InputTokens = intPtr(getInt(usage.InputTokens))
	}
	if usage.OutputTokens != nil {
		detail.OutputTokens = intPtr(getInt(usage.OutputTokens))
	}
	if usage.TotalTokens != nil {
		detail.TotalTokens = intPtr(getInt(usage.TotalTokens))
	}
	if usage.CacheHitTokens != nil {
		detail.CachedInputTokens = intPtr(getInt(usage.CacheHitTokens))
	}
	cacheCreation := int(numberFromUsageMap(raw, "cache_creation_input_tokens"))
	if creation, ok := raw["cache_creation"].(map[string]interface{}); ok && cacheCreation == 0 {
		cacheCreation = int(numberFromUsageMap(creation, "ephemeral_5m_input_tokens")) + int(numberFromUsageMap(creation, "ephemeral_1h_input_tokens"))
	}
	if cacheCreation > 0 {
		detail.CacheCreationInputTokens = intPtr(cacheCreation)
	}
	builtin := builtinToolUsage{}
	if tool, ok := raw["server_tool_use"].(map[string]interface{}); ok {
		builtin.WebSearchCalls = int(numberFromUsageMap(tool, "web_search_requests"))
	}
	return providerUsageResult{Usage: usage, Detail: detail, Builtin: builtin, Source: source, HasUsage: usageHasAnyTokens(usage) || builtin != (builtinToolUsage{})}
}

func usageHasAnyTokens(usage usageTokenUsage) bool {
	return usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil || usage.CacheHitTokens != nil
}

func setDetailInt(target **int, raw map[string]interface{}, key string) {
	if rawValue, ok := raw[key]; ok && rawValue != nil {
		*target = intPtr(int(numberFromUsageMap(raw, key)))
	}
}

func addGeminiTokenDetails(raw map[string]interface{}, key string, textTokens **int, imageTokens **int, audioTokens **int) {
	details, ok := raw[key].([]interface{})
	if !ok {
		return
	}
	textTotal := getInt(*textTokens)
	imageTotal := getInt(*imageTokens)
	audioTotal := getInt(*audioTokens)
	seenText := *textTokens != nil
	seenImage := *imageTokens != nil
	seenAudio := *audioTokens != nil
	for _, item := range details {
		detail, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		count := int(numberFromUsageMap(detail, "tokenCount"))
		switch strings.ToUpper(stringValueFromMap(detail, "modality")) {
		case "TEXT":
			textTotal += count
			seenText = true
		case "IMAGE":
			imageTotal += count
			seenImage = true
		case "AUDIO":
			audioTotal += count
			seenAudio = true
		}
	}
	if seenText {
		*textTokens = intPtr(textTotal)
	}
	if seenImage {
		*imageTokens = intPtr(imageTotal)
	}
	if seenAudio {
		*audioTokens = intPtr(audioTotal)
	}
}

func builtinToolUsageFromResponsesOutput(raw interface{}) builtinToolUsage {
	items, ok := raw.([]interface{})
	if !ok {
		return builtinToolUsage{}
	}
	usage := builtinToolUsage{}
	for _, item := range items {
		if itemMap, ok := item.(map[string]interface{}); ok {
			usage = mergeBuiltinToolUsage(usage, builtinToolUsageFromResponsesItem(itemMap))
		}
	}
	return usage
}

func builtinToolUsageFromResponsesItem(item map[string]interface{}) builtinToolUsage {
	switch stringValueFromMap(item, "type") {
	case "web_search_call":
		return builtinToolUsage{WebSearchCalls: 1}
	case "file_search_call":
		return builtinToolUsage{FileSearchCalls: 1}
	case "image_generation_call":
		return builtinToolUsage{ImageGenerationCalls: 1}
	case "code_interpreter_call":
		return builtinToolUsage{CodeInterpreterCalls: 1}
	case "computer_call", "computer_use":
		return builtinToolUsage{ComputerUseCalls: 1}
	default:
		return builtinToolUsage{}
	}
}

func mergeUsageDetail(existing usageDetail, next usageDetail) usageDetail {
	if next.InputTokens != nil {
		existing.InputTokens = next.InputTokens
	}
	if next.OutputTokens != nil {
		existing.OutputTokens = next.OutputTokens
	}
	if next.TotalTokens != nil {
		existing.TotalTokens = next.TotalTokens
	}
	if next.CachedInputTokens != nil {
		existing.CachedInputTokens = next.CachedInputTokens
	}
	if next.CacheCreationInputTokens != nil {
		existing.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	if next.ReasoningTokens != nil {
		existing.ReasoningTokens = next.ReasoningTokens
	}
	if next.TextInputTokens != nil {
		existing.TextInputTokens = next.TextInputTokens
	}
	if next.TextOutputTokens != nil {
		existing.TextOutputTokens = next.TextOutputTokens
	}
	if next.ImageInputTokens != nil {
		existing.ImageInputTokens = next.ImageInputTokens
	}
	if next.ImageOutputTokens != nil {
		existing.ImageOutputTokens = next.ImageOutputTokens
	}
	if next.AudioInputTokens != nil {
		existing.AudioInputTokens = next.AudioInputTokens
	}
	if next.AudioOutputTokens != nil {
		existing.AudioOutputTokens = next.AudioOutputTokens
	}
	if next.ToolUseTokens != nil {
		existing.ToolUseTokens = next.ToolUseTokens
	}
	if next.Estimated {
		existing.Estimated = true
	}
	return existing
}

func mergeBuiltinToolUsage(existing builtinToolUsage, next builtinToolUsage) builtinToolUsage {
	existing.WebSearchCalls += next.WebSearchCalls
	existing.FileSearchCalls += next.FileSearchCalls
	existing.ImageGenerationCalls += next.ImageGenerationCalls
	existing.CodeInterpreterCalls += next.CodeInterpreterCalls
	existing.ComputerUseCalls += next.ComputerUseCalls
	return existing
}

func stringValueFromMap(raw map[string]interface{}, key string) string {
	value, _ := raw[key].(string)
	return value
}

func mergeUsage(existing usageTokenUsage, next usageTokenUsage) usageTokenUsage {
	if next.InputTokens != nil {
		existing.InputTokens = next.InputTokens
	}
	if next.OutputTokens != nil {
		existing.OutputTokens = next.OutputTokens
	}
	if next.TotalTokens != nil {
		existing.TotalTokens = next.TotalTokens
	}
	if next.CacheHitTokens != nil {
		existing.CacheHitTokens = next.CacheHitTokens
	}
	if next.EstimatedTokens != 0 {
		existing.EstimatedTokens = next.EstimatedTokens
	}
	if next.Estimated {
		existing.Estimated = true
	}
	if existing.TotalTokens == nil && existing.InputTokens != nil && existing.OutputTokens != nil {
		existing.TotalTokens = intPtr(getInt(existing.InputTokens) + getInt(existing.OutputTokens))
	}
	return existing
}

func getInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func (s *Server) recordUsage(record *usageRecord) {
	if record == nil {
		return
	}
	// 回读「返回下游」内容（第四段链路）。capture writer tee 了实际写给客户端的字节。
	// 仅在还没显式设置过时回填，避免覆盖特殊路径手动赋的值。
	if record.downstream != nil && record.DownstreamResponse.Content == "" {
		record.DownstreamResponse = record.downstream.downstreamBody()
	}
	if record.EndedAt.IsZero() {
		record.EndedAt = time.Now()
	}
	if record.DurationMs == 0 {
		record.DurationMs = record.EndedAt.Sub(record.StartedAt).Milliseconds()
	}

	if s.store != nil {
		// 优先异步落库，避免请求路径阻塞在 SQLite 写入上；
		// 队列满或未启动时降级为同步写，保证不丢记录。
		if s.enqueueUsageRecord(record) {
			return
		}
		if err := s.saveUsageRecordToStore(record); err != nil {
			log.Printf("failed to save usage record to sqlite: %v", err)
		}
		return
	}

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	s.usageRecords = append(s.usageRecords, *record)
	s.appendUsageRecordLocked(*record)
	s.compactUsageRecordsLocked()
}

func (s *Server) usageSnapshot() []usageRecord {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	snapshot := make([]usageRecord, len(s.usageRecords))
	copy(snapshot, s.usageRecords)
	return snapshot
}

func (s *Server) usageDashboard(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(usageDashboardHTML))
}

func (s *Server) usageStats(c *gin.Context) {
	from, to := usageTimeRange(c)
	window := usageWindow(c.Query("window"), from, to)
	if s.store != nil {
		query := usageQueryFromRequest(c)
		summary, err := s.store.UsageTotals(c.Request.Context(), query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// allTimeSummary 是"累计"口径：不带任何过滤再查一次，
		// 与非 store 分支的 summarizeUsage(snapshot) 语义保持一致。
		allTimeSummary, err := s.store.UsageTotals(c.Request.Context(), storage.UsageQuery{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "window": window, "summary": summary, "allTimeSummary": allTimeSummary})
		return
	}
	snapshot := s.usageSnapshot()
	records := filterUsageRecords(snapshot, c, from, to)

	response := gin.H{
		"from":           from,
		"to":             to,
		"window":         window,
		"summary":        summarizeUsage(records),
		"allTimeSummary": summarizeUsage(snapshot),
		"series":         aggregateUsage(records, "window", window),
		"chartSeries":    aggregateUsageSeries(records, window, from, to),
		"byCaller":       aggregateUsage(records, "key", window),
		"byKey":          aggregateUsage(records, "key", window),
		"byModelGroup":   aggregateUsage(records, "modelGroup", window),
		"byModel":        aggregateUsage(records, "model", window),
		"bySourceFormat": aggregateUsage(records, "sourceFormat", window),
		"byTargetFormat": aggregateUsage(records, "targetFormat", window),
		"byRelayMode":    aggregateUsage(records, "relayMode", window),
		"byUsageSource":  aggregateUsage(records, "usageSource", window),
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) usageLogs(c *gin.Context) {
	if s.store != nil {
		total, items, err := s.store.QueryUsageLogs(c.Request.Context(), usageQueryFromRequest(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"total": total, "items": items})
		return
	}
	from, to := usageTimeRange(c)
	records := filterUsageRecords(s.usageSnapshot(), c, from, to)
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt.After(records[j].StartedAt) })

	limit := parsePositiveInt(c.Query("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	offset := parsePositiveInt(c.Query("offset"), 0)
	if offset > len(records) {
		offset = len(records)
	}
	end := offset + limit
	if end > len(records) {
		end = len(records)
	}

	items := make([]gin.H, 0, end-offset)
	for _, record := range records[offset:end] {
		items = append(items, gin.H{
			"requestId":                 record.RequestID,
			"startedAt":                 record.StartedAt,
			"keyName":                   record.KeyName,
			"keyHash":                   record.KeyHash,
			"groupName":                 record.GroupName,
			"modelName":                 record.ModelName,
			"platform":                  record.Platform,
			"sourceFormat":              record.SourceFormat,
			"targetFormat":              record.TargetFormat,
			"relayMode":                 record.RelayMode,
			"responsesMode":             record.ResponsesMode,
			"usageSource":               record.UsageSource,
			"stream":                    record.Stream,
			"statusCode":                record.StatusCode,
			"error":                     record.Error,
			"firstByteMs":               record.FirstByteMs,
			"durationMs":                record.DurationMs,
			"usage":                     record.Usage,
			"usageDetail":               record.UsageDetail,
			"builtinToolUsage":          record.BuiltinToolUsage,
			"requestWarnings":           record.RequestWarnings,
			"retryCount":                record.RetryCount,
			"incomingBodyTruncated":     record.IncomingBody.Truncated,
			"outgoingBodyTruncated":     record.OutgoingBody.Truncated,
			"providerResponseTruncated": record.ProviderResponse.Truncated,
		})
	}

	c.JSON(http.StatusOK, gin.H{"total": len(records), "items": items})
}

func (s *Server) usageLogDetail(c *gin.Context) {
	id := c.Param("id")
	if s.store != nil {
		payload, found, err := s.store.GetUsageRecordJSON(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "usage log not found"})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
		return
	}
	for _, record := range s.usageSnapshot() {
		if record.RequestID == id {
			c.JSON(http.StatusOK, record)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "usage log not found"})
}

func (s *Server) resetUsage(c *gin.Context) {
	if s.store != nil {
		if err := s.store.ClearUsage(c.Request.Context()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"reset": true})
		return
	}
	if err := s.clearUsageRecords(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

func usageTimeRange(c *gin.Context) (time.Time, time.Time) {
	var from time.Time
	to := time.Now()
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			to = parsed
		}
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			from = parsed
		}
	}
	return from, to
}

func usageWindow(raw string, from, to time.Time) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "5m", "15m", "hour", "day":
		return strings.ToLower(strings.TrimSpace(raw))
	case "minute":
		return "5m"
	}

	if from.IsZero() {
		return "day"
	}

	duration := to.Sub(from)
	if duration <= 24*time.Hour {
		return "5m"
	}
	if duration <= 7*24*time.Hour {
		return "hour"
	}
	return "day"
}

func filterUsageRecords(records []usageRecord, c *gin.Context, from, to time.Time) []usageRecord {
	result := make([]usageRecord, 0, len(records))
	keyNames := c.QueryArray("keyName")
	groupNames := c.QueryArray("groupName")
	modelNames := c.QueryArray("modelName")
	stream := strings.TrimSpace(c.Query("stream"))
	status := strings.TrimSpace(c.Query("status"))
	for _, record := range records {
		if (!from.IsZero() && record.StartedAt.Before(from)) || (!to.IsZero() && record.StartedAt.After(to)) {
			continue
		}
		if !usageValueMatches(keyNames, record.KeyName) {
			continue
		}
		if !usageValueMatches(groupNames, record.GroupName) {
			continue
		}
		if !usageValueMatches(modelNames, record.ModelName) {
			continue
		}
		if stream != "" && strconv.FormatBool(record.Stream) != strings.ToLower(stream) {
			continue
		}
		if status != "" && !usageStatusMatches(record.StatusCode, status) {
			continue
		}
		result = append(result, record)
	}
	return result
}

func usageValueMatches(filters []string, value string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if strings.TrimSpace(filter) == value {
			return true
		}
	}
	return false
}

func usageStatusMatches(statusCode int, filter string) bool {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "":
		return true
	case "success":
		return statusCode >= 200 && statusCode < 400
	case "failed":
		return statusCode < 200 || statusCode >= 400
	default:
		code, err := strconv.Atoi(filter)
		return err == nil && statusCode == code
	}
}

func summarizeUsage(records []usageRecord) usageSummary {
	var summary usageSummary
	firstByteSamples := make([]int64, 0, len(records))
	durationSamples := make([]int64, 0, len(records))
	latencySamples := make([]int64, 0, len(records))
	for _, record := range records {
		addRecordToSummary(&summary, record)
		if record.FirstByteMs > 0 {
			firstByteSamples = append(firstByteSamples, record.FirstByteMs)
		}
		if record.DurationMs > 0 {
			durationSamples = append(durationSamples, record.DurationMs)
		}
		if record.FirstByteMs > 0 && record.DurationMs > 0 {
			latencySamples = append(latencySamples, record.FirstByteMs+record.DurationMs)
		}
	}
	finalizeUsageSummary(&summary, firstByteSamples, durationSamples, latencySamples)
	return summary
}

func finalizeUsageSummary(summary *usageSummary, firstByteSamples []int64, durationSamples []int64, latencySamples []int64) {
	if summary.Requests > 0 {
		summary.SuccessRate = float64(summary.Success) / float64(summary.Requests)
		summary.FailedRate = float64(summary.Failed) / float64(summary.Requests)
		summary.AvgInputTokens = float64(summary.InputTokens) / float64(summary.Requests)
		summary.AvgOutputTokens = float64(summary.OutputTokens) / float64(summary.Requests)
		summary.AvgTotalTokens = float64(summary.TotalTokens) / float64(summary.Requests)
	}
	if summary.InputTokens > 0 {
		summary.CacheHitRate = float64(summary.CacheHitTokens) / float64(summary.InputTokens)
	}
	if len(firstByteSamples) > 0 {
		summary.AvgFirstByteMs = avgInt64(firstByteSamples)
		summary.P95FirstByteMs = percentileInt64(firstByteSamples, 0.95)
	}
	if len(durationSamples) > 0 {
		summary.AvgDurationMs = avgInt64(durationSamples)
		summary.P95DurationMs = percentileInt64(durationSamples, 0.95)
	}
	if len(latencySamples) > 0 {
		summary.AvgLatencyMs = avgInt64(latencySamples)
	}
}

func avgInt64(values []int64) float64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return float64(total) / float64(len(values))
}

func percentileInt64(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * percentile)
	return sorted[idx]
}

func addRecordToSummary(summary *usageSummary, record usageRecord) {
	summary.Requests++
	if record.StatusCode >= 200 && record.StatusCode < 400 {
		summary.Success++
	} else {
		summary.Failed++
	}
	if record.Stream {
		summary.StreamRequests++
	}
	if record.Usage.Estimated {
		summary.EstimatedRequests++
	}
	summary.InputTokens += getInt(record.Usage.InputTokens)
	summary.OutputTokens += getInt(record.Usage.OutputTokens)
	summary.TotalTokens += getInt(record.Usage.TotalTokens)
	summary.CacheHitTokens += getInt(record.Usage.CacheHitTokens)
	summary.EstimatedTokens += record.Usage.EstimatedTokens
	summary.ReasoningTokens += getInt(record.UsageDetail.ReasoningTokens)
	summary.WebSearchCalls += record.BuiltinToolUsage.WebSearchCalls
	summary.FileSearchCalls += record.BuiltinToolUsage.FileSearchCalls
	summary.ImageGenerationCalls += record.BuiltinToolUsage.ImageGenerationCalls
	switch record.SourceFormat {
	case string(relay.FormatOpenAIChat), string(relay.FormatOpenAI):
		summary.OpenAIChatRequests++
	case string(relay.FormatClaude):
		summary.ClaudeRequests++
	case string(relay.FormatGemini):
		summary.GeminiRequests++
	case string(relay.FormatResponses):
		summary.ResponsesRequests++
	}
	switch record.ResponsesMode {
	case "native_responses":
		summary.NativeResponsesRequests++
	case "transformed_responses":
		summary.TransformedResponsesRequests++
	}
	if summary.FirstUsedAt.IsZero() || record.StartedAt.Before(summary.FirstUsedAt) {
		summary.FirstUsedAt = record.StartedAt
		summary.FirstModelName = record.ModelName
	}
	if record.StartedAt.After(summary.LastUsedAt) {
		summary.LastUsedAt = record.StartedAt
		summary.LastModelName = record.ModelName
	}
}

func aggregateUsage(records []usageRecord, dimension string, window string) []usageAggregate {
	groups := map[string]*usageAggregate{}
	firstByteSamples := map[string][]int64{}
	durationSamples := map[string][]int64{}
	latencySamples := map[string][]int64{}
	for _, record := range records {
		key := aggregateKey(record, dimension, window)
		if key == "" {
			continue
		}
		item := groups[key]
		if item == nil {
			item = &usageAggregate{Key: key}
			fillAggregateLabels(item, record, dimension, key)
			groups[key] = item
		}
		addRecordToSummary(&item.UsageSummary, record)
		if record.FirstByteMs > 0 {
			firstByteSamples[key] = append(firstByteSamples[key], record.FirstByteMs)
		}
		if record.DurationMs > 0 {
			durationSamples[key] = append(durationSamples[key], record.DurationMs)
		}
		if record.FirstByteMs > 0 && record.DurationMs > 0 {
			latencySamples[key] = append(latencySamples[key], record.FirstByteMs+record.DurationMs)
		}
	}

	items := make([]usageAggregate, 0, len(groups))
	for key, item := range groups {
		finalizeUsageSummary(&item.UsageSummary, firstByteSamples[key], durationSamples[key], latencySamples[key])
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

func aggregateUsageSeries(records []usageRecord, window string, from, to time.Time) []usageAggregate {
	aggregates := aggregateUsage(records, "window", window)
	byWindow := make(map[string]usageAggregate, len(aggregates))
	for _, item := range aggregates {
		byWindow[item.Key] = item
	}

	location := time.Local
	if len(records) > 0 {
		location = records[0].StartedAt.Location()
	}
	startFrom := from
	if startFrom.IsZero() {
		if len(records) > 0 {
			earliest := records[0].StartedAt
			for _, r := range records {
				if r.StartedAt.Before(earliest) {
					earliest = r.StartedAt
				}
			}
			startFrom = earliest
		} else {
			startFrom = to
		}
	}
	start := truncateUsageWindow(startFrom.In(location), window)
	end := truncateUsageWindow(to.In(location), window)
	series := make([]usageAggregate, 0)
	for current := start; !current.After(end); current = nextUsageWindow(current, window) {
		key := current.Format(time.RFC3339)
		if item, ok := byWindow[key]; ok {
			series = append(series, item)
			continue
		}
		series = append(series, usageAggregate{Key: key, Window: key})
	}
	return series
}

func nextUsageWindow(t time.Time, window string) time.Time {
	switch window {
	case "5m":
		return t.Add(5 * time.Minute)
	case "15m":
		return t.Add(15 * time.Minute)
	case "day":
		return t.AddDate(0, 0, 1)
	default:
		return t.Add(time.Hour)
	}
}

func aggregateKey(record usageRecord, dimension string, window string) string {
	switch dimension {
	case "key":
		if record.KeyName != "" {
			return record.KeyName + " (" + record.KeyHash + ")"
		}
		return record.KeyHash
	case "modelGroup":
		return record.GroupName
	case "model":
		return record.GroupName + " / " + record.ModelName
	case "sourceFormat":
		if record.SourceFormat != "" {
			return record.SourceFormat
		}
		return record.InputFormat
	case "targetFormat":
		return record.TargetFormat
	case "relayMode":
		return record.RelayMode
	case "usageSource":
		return record.UsageSource
	case "window":
		return truncateUsageWindow(record.StartedAt, window).Format(time.RFC3339)
	default:
		return ""
	}
}

func fillAggregateLabels(item *usageAggregate, record usageRecord, dimension string, key string) {
	switch dimension {
	case "key":
		item.KeyName = record.KeyName
		item.KeyHash = record.KeyHash
	case "modelGroup":
		item.GroupName = record.GroupName
	case "model":
		item.GroupName = record.GroupName
		item.ModelName = record.ModelName
		item.Platform = record.Platform
	case "window":
		item.Window = key
	}
}

func truncateUsageWindow(t time.Time, window string) time.Time {
	// 按本地时钟对齐窗口边界：t.Truncate 按 Unix 纪元取整，在非整小时时区
	//（如 UTC+5:30）会把"小时"桶切在半点上，图表标签与数据错位。
	location := t.Location()
	switch window {
	case "5m":
		minute := t.Minute() - t.Minute()%5
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), minute, 0, 0, location)
	case "15m":
		minute := t.Minute() - t.Minute()%15
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), minute, 0, 0, location)
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, location)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, location)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, location)
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

type observingStreamWriter struct {
	inner        relay.StreamResponseWriter
	record       *usageRecord
	startTime    time.Time
	events       []json.RawMessage
	responseText strings.Builder
	observeUsage bool
}

func (w *observingStreamWriter) Write(data []byte) (int, error) {
	w.observe(data)
	return w.inner.Write(data)
}

func (w *observingStreamWriter) WriteString(data string) (int, error) {
	w.observe([]byte(data))
	return w.inner.WriteString(data)
}

func (w *observingStreamWriter) Flush() error {
	return w.inner.Flush()
}

func (w *observingStreamWriter) observe(data []byte) {
	if w.record == nil {
		return
	}
	if w.record.FirstByteMs == 0 && len(strings.TrimSpace(string(data))) > 0 {
		w.record.FirstByteMs = time.Since(w.startTime).Milliseconds()
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		w.responseText.WriteString(extractOutputTextFromStreamPayload(payload))
		if !w.observeUsage {
			continue
		}
		if len(w.events) < 50 {
			w.events = append(w.events, json.RawMessage(payload))
			if eventBytes, err := json.Marshal(w.events); err == nil {
				w.record.ProviderResponse = sanitizeUsageBody(eventBytes)
			}
		}
		result := extractProviderUsageFromStreamEvent("", relay.FormatResponses, payload)
		applyProviderUsageToRecord(w.record, result)
	}
}

type upstreamUsageObservingBody struct {
	inner    io.ReadCloser
	record   *usageRecord
	platform relay.Platform
	format   relay.FormatType
	buffer   []byte
	events   []json.RawMessage
}

func observeUpstreamUsage(resp *http.Response, record *usageRecord, platform relay.Platform, formats ...relay.FormatType) {
	if resp == nil || resp.Body == nil || record == nil {
		return
	}
	format := relay.FormatType("")
	if len(formats) > 0 {
		format = formats[0]
	}
	resp.Body = &upstreamUsageObservingBody{inner: resp.Body, record: record, platform: platform, format: format}
}

func (b *upstreamUsageObservingBody) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	if n > 0 {
		b.observe(p[:n])
	}
	return n, err
}

func (b *upstreamUsageObservingBody) Close() error {
	if line := strings.TrimSpace(string(b.buffer)); line != "" {
		b.observeLine(line)
		b.buffer = nil
	}
	return b.inner.Close()
}

func (b *upstreamUsageObservingBody) observe(data []byte) {
	b.buffer = append(b.buffer, data...)
	for {
		idx := bytes.IndexByte(b.buffer, '\n')
		if idx < 0 {
			return
		}
		line := strings.TrimSpace(string(b.buffer[:idx]))
		b.buffer = b.buffer[idx+1:]
		b.observeLine(line)
	}
}

func (b *upstreamUsageObservingBody) observeLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	if len(b.events) < 50 {
		b.events = append(b.events, json.RawMessage(payload))
		if eventBytes, err := json.Marshal(b.events); err == nil {
			b.record.ProviderResponse = sanitizeUsageBody(eventBytes)
		}
	}
	result := extractProviderUsageFromStreamEvent(b.platform, b.format, payload)
	applyProviderUsageToRecord(b.record, result)
}

func parsePlatformStreamUsage(platform relay.Platform, payload string) (usageTokenUsage, bool) {
	result := extractProviderUsageFromStreamEvent(platform, "", payload)
	return result.Usage, usageHasAnyTokens(result.Usage)
}

func parseStreamUsage(payload string) (usageTokenUsage, bool) {
	result := extractProviderUsageFromStreamEvent("", relay.FormatResponses, payload)
	return result.Usage, usageHasAnyTokens(result.Usage)
}

func usageFromOpenAIUsage(raw map[string]interface{}) usageTokenUsage {
	usage := usageTokenUsage{}
	if rawValue, ok := raw["prompt_tokens"]; ok && rawValue != nil {
		usage.InputTokens = intPtr(int(numberFromUsageMap(raw, "prompt_tokens")))
	} else if rawValue, ok := raw["input_tokens"]; ok && rawValue != nil {
		usage.InputTokens = intPtr(int(numberFromUsageMap(raw, "input_tokens")))
	}
	if rawValue, ok := raw["completion_tokens"]; ok && rawValue != nil {
		usage.OutputTokens = intPtr(int(numberFromUsageMap(raw, "completion_tokens")))
	} else if rawValue, ok := raw["output_tokens"]; ok && rawValue != nil {
		usage.OutputTokens = intPtr(int(numberFromUsageMap(raw, "output_tokens")))
	}
	if rawValue, ok := raw["total_tokens"]; ok && rawValue != nil {
		usage.TotalTokens = intPtr(int(numberFromUsageMap(raw, "total_tokens")))
	}
	cacheHitTokens := maxInt(int(numberFromUsageMap(raw, "cached_tokens")), int(numberFromUsageMap(raw, "prompt_cache_hit_tokens")))
	cacheFieldSeen := false
	if rawValue, ok := raw["cached_tokens"]; ok && rawValue != nil {
		cacheFieldSeen = true
	}
	if rawValue, ok := raw["prompt_cache_hit_tokens"]; ok && rawValue != nil {
		cacheFieldSeen = true
	}
	if details, ok := raw["prompt_tokens_details"].(map[string]interface{}); ok {
		cacheHitTokens = maxInt(cacheHitTokens, int(numberFromUsageMap(details, "cached_tokens")), int(numberFromUsageMap(details, "cache_read_tokens")))
		if rawValue, ok := details["cached_tokens"]; ok && rawValue != nil {
			cacheFieldSeen = true
		}
		if rawValue, ok := details["cache_read_tokens"]; ok && rawValue != nil {
			cacheFieldSeen = true
		}
	}
	if details, ok := raw["input_tokens_details"].(map[string]interface{}); ok {
		cacheHitTokens = maxInt(cacheHitTokens, int(numberFromUsageMap(details, "cached_tokens")), int(numberFromUsageMap(details, "cache_read_tokens")))
		if rawValue, ok := details["cached_tokens"]; ok && rawValue != nil {
			cacheFieldSeen = true
		}
		if rawValue, ok := details["cache_read_tokens"]; ok && rawValue != nil {
			cacheFieldSeen = true
		}
	}
	if cacheFieldSeen || cacheHitTokens > 0 {
		usage.CacheHitTokens = intPtr(cacheHitTokens)
	}
	if usage.TotalTokens == nil && usage.InputTokens != nil && usage.OutputTokens != nil {
		usage.TotalTokens = intPtr(getInt(usage.InputTokens) + getInt(usage.OutputTokens))
	}
	return usage
}

func usageFromGeminiUsageMetadata(raw map[string]interface{}) usageTokenUsage {
	usage := usageTokenUsage{}
	inputTokens := 0
	inputSeen := false
	if rawValue, ok := raw["promptTokenCount"]; ok && rawValue != nil {
		inputTokens += int(numberFromUsageMap(raw, "promptTokenCount"))
		inputSeen = true
	}
	if rawValue, ok := raw["toolUsePromptTokenCount"]; ok && rawValue != nil {
		inputTokens += int(numberFromUsageMap(raw, "toolUsePromptTokenCount"))
		inputSeen = true
	}
	if inputSeen {
		usage.InputTokens = intPtr(inputTokens)
	}

	outputTokens := 0
	outputSeen := false
	if rawValue, ok := raw["candidatesTokenCount"]; ok && rawValue != nil {
		outputTokens += int(numberFromUsageMap(raw, "candidatesTokenCount"))
		outputSeen = true
	}
	if rawValue, ok := raw["thoughtsTokenCount"]; ok && rawValue != nil {
		outputTokens += int(numberFromUsageMap(raw, "thoughtsTokenCount"))
		outputSeen = true
	}
	if outputSeen {
		usage.OutputTokens = intPtr(outputTokens)
	}

	if rawValue, ok := raw["totalTokenCount"]; ok && rawValue != nil {
		usage.TotalTokens = intPtr(int(numberFromUsageMap(raw, "totalTokenCount")))
	}
	if rawValue, ok := raw["cachedContentTokenCount"]; ok && rawValue != nil {
		usage.CacheHitTokens = intPtr(int(numberFromUsageMap(raw, "cachedContentTokenCount")))
	}
	if usage.TotalTokens == nil && usage.InputTokens != nil && usage.OutputTokens != nil {
		usage.TotalTokens = intPtr(getInt(usage.InputTokens) + getInt(usage.OutputTokens))
	}
	return usage
}

func usageFromClaudeUsage(raw map[string]interface{}) usageTokenUsage {
	usage := usageTokenUsage{}
	inputTokens := 0
	inputSeen := false
	if rawValue, ok := raw["input_tokens"]; ok && rawValue != nil {
		inputTokens += int(numberFromUsageMap(raw, "input_tokens"))
		inputSeen = true
	}
	cacheReadTokens := 0
	if rawValue, ok := raw["cache_read_input_tokens"]; ok && rawValue != nil {
		cacheReadTokens = int(numberFromUsageMap(raw, "cache_read_input_tokens"))
		usage.CacheHitTokens = intPtr(cacheReadTokens)
		inputTokens += cacheReadTokens
		inputSeen = true
	}
	cacheCreationTokens := 0
	if rawValue, ok := raw["cache_creation_input_tokens"]; ok && rawValue != nil {
		cacheCreationTokens = int(numberFromUsageMap(raw, "cache_creation_input_tokens"))
	}
	if creation, ok := raw["cache_creation"].(map[string]interface{}); ok && cacheCreationTokens == 0 {
		cacheCreationTokens = int(numberFromUsageMap(creation, "ephemeral_5m_input_tokens")) + int(numberFromUsageMap(creation, "ephemeral_1h_input_tokens"))
	}
	if cacheCreationTokens > 0 {
		inputTokens += cacheCreationTokens
		inputSeen = true
	}
	if inputSeen {
		usage.InputTokens = intPtr(inputTokens)
	}
	if rawValue, ok := raw["output_tokens"]; ok && rawValue != nil {
		usage.OutputTokens = intPtr(int(numberFromUsageMap(raw, "output_tokens")))
	}
	if usage.InputTokens != nil && usage.OutputTokens != nil {
		usage.TotalTokens = intPtr(getInt(usage.InputTokens) + getInt(usage.OutputTokens))
	}
	return usage
}

func applyLocalResponseEstimate(record *usageRecord, responseText string, cfg config.UsageConfig) {
	if record == nil || strings.TrimSpace(responseText) == "" || record.Usage.OutputTokens != nil {
		return
	}
	outputTokens := estimateTextTokens(responseText, cfg)
	if outputTokens <= 0 {
		return
	}
	record.Usage.OutputTokens = intPtr(outputTokens)
	if record.Usage.TotalTokens == nil {
		record.Usage.TotalTokens = intPtr(getInt(record.Usage.InputTokens) + outputTokens)
	}
	record.Usage.Estimated = true
	if record.Usage.EstimatedTokens == 0 {
		record.Usage.EstimatedTokens = getInt(record.Usage.TotalTokens)
	}
	record.UsageDetail = mergeUsageDetail(record.UsageDetail, usageDetail{OutputTokens: intPtr(outputTokens), TotalTokens: record.Usage.TotalTokens, Estimated: true})
	record.UsageSource = "local_response_estimate"
}

func estimateTextTokens(text string, cfg config.UsageConfig) int {
	charsPerToken := cfg.CharsPerToken
	if charsPerToken <= 0 {
		charsPerToken = 4
	}
	chars := len([]rune(text))
	if chars == 0 {
		return 0
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

func extractOutputTextFromCanonicalResponse(resp *relay.CanonicalResponse) string {
	if resp == nil {
		return ""
	}
	var builder strings.Builder
	for _, item := range resp.Output {
		for _, part := range item.Content {
			builder.WriteString(part.Text)
			builder.WriteString(part.ReasoningText)
		}
		if len(item.Arguments) > 0 {
			builder.Write(item.Arguments)
		}
	}
	return builder.String()
}

func extractOutputTextFromProviderBody(platform relay.Platform, format relay.FormatType, body []byte) string {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return extractOutputTextFromPayload(payload)
}

func extractOutputTextFromStreamPayload(payload string) string {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return ""
	}
	return extractOutputTextFromPayload(event)
}

func extractOutputTextFromPayload(payload map[string]interface{}) string {
	var builder strings.Builder
	if choices, ok := payload["choices"].([]interface{}); ok {
		for _, choice := range choices {
			choiceMap, _ := choice.(map[string]interface{})
			if delta, ok := choiceMap["delta"].(map[string]interface{}); ok {
				builder.WriteString(stringValueFromMap(delta, "content"))
			}
			if msg, ok := choiceMap["message"].(map[string]interface{}); ok {
				builder.WriteString(extractTextFromAny(msg["content"]))
			}
		}
	}
	if delta := stringValueFromMap(payload, "delta"); delta != "" && strings.Contains(stringValueFromMap(payload, "type"), "output_text") {
		builder.WriteString(delta)
	}
	if text := stringValueFromMap(payload, "text"); text != "" && strings.Contains(stringValueFromMap(payload, "type"), "output_text") {
		builder.WriteString(text)
	}
	if raw, ok := payload["delta"].(map[string]interface{}); ok {
		builder.WriteString(stringValueFromMap(raw, "text"))
		builder.WriteString(stringValueFromMap(raw, "thinking"))
	}
	if response, ok := payload["response"].(map[string]interface{}); ok {
		builder.WriteString(extractTextFromResponsesOutput(response["output"]))
	}
	builder.WriteString(extractTextFromResponsesOutput(payload["output"]))
	if content, ok := payload["content"].([]interface{}); ok {
		for _, item := range content {
			if itemMap, ok := item.(map[string]interface{}); ok {
				builder.WriteString(stringValueFromMap(itemMap, "text"))
			}
		}
	}
	if candidates, ok := payload["candidates"].([]interface{}); ok {
		for _, candidate := range candidates {
			candMap, _ := candidate.(map[string]interface{})
			if content, ok := candMap["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok {
					for _, part := range parts {
						if partMap, ok := part.(map[string]interface{}); ok {
							builder.WriteString(stringValueFromMap(partMap, "text"))
						}
					}
				}
			}
		}
	}
	return builder.String()
}

func extractTextFromResponsesOutput(raw interface{}) string {
	items, ok := raw.([]interface{})
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if content, ok := itemMap["content"].([]interface{}); ok {
			for _, part := range content {
				if partMap, ok := part.(map[string]interface{}); ok {
					builder.WriteString(stringValueFromMap(partMap, "text"))
				}
			}
		}
	}
	return builder.String()
}

func extractTextFromAny(raw interface{}) string {
	switch value := raw.(type) {
	case string:
		return value
	case []interface{}:
		var builder strings.Builder
		for _, item := range value {
			if itemMap, ok := item.(map[string]interface{}); ok {
				builder.WriteString(stringValueFromMap(itemMap, "text"))
			}
		}
		return builder.String()
	default:
		return ""
	}
}

func numberFromUsageMap(raw map[string]interface{}, key string) float64 {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		parsed, _ := v.Float64()
		return parsed
	default:
		return 0
	}
}

func setRecordGroup(record *usageRecord, group *config.ModelGroupConfig) {
	if record == nil || group == nil {
		return
	}
	record.GroupID = group.ID
	record.GroupName = group.Name
	record.RequestedModelGroup = group.Name
}

func setRecordModel(record *usageRecord, model config.ModelRef, platform relay.Platform) {
	if record == nil {
		return
	}
	record.ModelID = model.ID
	record.ModelName = model.Name
	record.Platform = model.Platform
	record.TargetPlatform = string(platform)
}
