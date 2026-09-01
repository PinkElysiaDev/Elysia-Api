package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	RequestID           string    `json:"requestId"`
	StartedAt           time.Time `json:"startedAt"`
	EndedAt             time.Time `json:"endedAt"`
	KeyName             string    `json:"keyName"`
	KeyHash             string    `json:"keyHash"`
	RequestedModelGroup string    `json:"requestedModelGroup"`
	GroupID             string    `json:"groupId"`
	GroupName           string    `json:"groupName"`
	ModelID             string    `json:"modelId"`
	ModelName           string    `json:"modelName"`
	SourceID            string    `json:"sourceId,omitempty"`
	Platform            string    `json:"platform"`
	InputFormat         string    `json:"inputFormat"`
	TargetPlatform      string    `json:"targetPlatform"`
	SourceFormat        string    `json:"sourceFormat,omitempty"`
	TargetFormat        string    `json:"targetFormat,omitempty"`
	SourceEndpoint      string    `json:"sourceEndpoint,omitempty"`
	TargetEndpoint      string    `json:"targetEndpoint,omitempty"`
	RelayMode           string    `json:"relayMode,omitempty"`
	ResponsesMode       string    `json:"responsesMode,omitempty"`
	ConversionChain     []string  `json:"conversionChain,omitempty"`
	UsageSource         string    `json:"usageSource,omitempty"`
	RequestWarnings     []string  `json:"requestWarnings,omitempty"`
	Stream              bool      `json:"stream"`
	StatusCode          int       `json:"statusCode"`
	Error               string    `json:"error,omitempty"`
	// ErrorKind 是错误归类（ErrorKind* 常量），供面板筛选/展示；空表示未归类。
	ErrorKind          string           `json:"errorKind,omitempty"`
	FirstByteMs        int64            `json:"firstByteMs"`
	DurationMs         int64            `json:"durationMs"`
	Usage              usageTokenUsage  `json:"usage"`
	UsageDetail        usageDetail      `json:"usageDetail,omitempty"`
	BuiltinToolUsage   builtinToolUsage `json:"builtinToolUsage,omitempty"`
	RetryCount         int              `json:"retryCount"`
	RetryEvents        []retryEvent     `json:"retryEvents"`
	IncomingBody       usageBody        `json:"incomingBody"`
	OutgoingBody       usageBody        `json:"outgoingBody"`
	ProviderResponse   usageBody        `json:"providerResponse"`
	DownstreamResponse usageBody        `json:"downstreamResponse"`

	// downstream 是写回下游客户端的 ResponseWriter 捕获器，运行期内部使用，
	// 不参与 JSON 序列化。recordUsage 会从它回读 DownstreamResponse。
	downstream *downstreamCaptureWriter `json:"-"`
	// writeGen 与 Server.usageWriteGen 对齐；reset 递增后丢弃更早的写入。
	writeGen uint64 `json:"-"`
	// bodyOpts 是本条请求生效的日志内容策略（initUsageRecord 从配置快照一次，
	// 四段 body 共用，避免热更新导致同一请求各段口径不一致）。
	bodyOpts usageBodyOptions `json:"-"`
	// assets 收集四段 body 中外置的 base64 媒体（捕获期登记、落库期写盘）。
	assets assetSink `json:"-"`
	// pendingStreamEvents 是流式请求捕获的上游事件（环形保留最后
	// StreamEventsCacheMax 条），recordUsage 物化为 ProviderResponse。
	pendingStreamEvents []json.RawMessage `json:"-"`
}

// usageBodyOptions 是单条请求生效的日志内容策略。initialized=false 表示
// 未初始化（直接构造的裸记录，多见于测试），按历史默认 1MiB、不外置处理——
// 不能靠 maxBytes 零值判断，因为 0 是显式的「不保存任何请求体」。
type usageBodyOptions struct {
	initialized bool
	maxBytes    int
	externalize bool
}

// effectiveMaxBytes 归一化上限：未初始化走 UsageBodyMaxBytes 历史默认。
func (o usageBodyOptions) effectiveMaxBytes() int {
	if !o.initialized {
		return UsageBodyMaxBytes
	}
	return o.maxBytes
}

func shortTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

func (s *Server) initUsageRecord(c *gin.Context, start time.Time, body []byte, inputFormat relay.FormatType) *usageRecord {
	cfg := s.usageLogConfig()
	requestID := usageRequestID(start)
	record := &usageRecord{
		RequestID:   requestID,
		StartedAt:   start,
		KeyName:     c.GetString("elysiaKeyName"),
		KeyHash:     c.GetString("elysiaKeyHash"),
		InputFormat: string(inputFormat),
		StatusCode:  http.StatusOK,
		bodyOpts:    usageBodyOptions{initialized: true, maxBytes: cfg.BodyMaxBytes, externalize: cfg.ExternalizeMedia},
		assets:      newAssetSink(requestID),
	}
	record.IncomingBody = record.sanitizeBody(body)
	return record
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

// sanitizeBody 是四段链路共用的请求体清洗入口：解析 → 脱敏 → 媒体外置 → 截断。
// 顺序关键：外置必须先于截断，否则大请求体会被拦腰截成非法 JSON，其中的
// base64 媒体也永远提不出来。maxBytes 显式为 0（不保存请求体）时返回空体；
// JSON 不可解析（非 JSON body）时退化为字节截断，保持历史语义。
func (r *usageRecord) sanitizeBody(data []byte) usageBody {
	maxBytes := r.bodyOpts.effectiveMaxBytes()
	if maxBytes == 0 || len(data) == 0 {
		return usageBody{}
	}
	var value interface{}
	if err := json.Unmarshal(data, &value); err == nil {
		redactJSON(value)
		if r.bodyOpts.externalize {
			r.assets.extractFromValue(value)
		}
		if sanitized, err := json.Marshal(value); err == nil {
			return truncateUsageBody(string(sanitized), maxBytes)
		}
	}
	if len(data) > maxBytes {
		return usageBody{Content: string(data[:maxBytes]), Truncated: true}
	}
	return usageBody{Content: string(data)}
}

// finalizeDownstreamBody 处理第四段「返回下游」：tee 捕获的是流式原始字节，
// 这里按「整体 JSON → SSE 逐行 → 字节截断」三级降级做外置与截断。
// 与前三段不同，下游内容不做脱敏（沿用 downstreamBody 的既定语义）。
func (r *usageRecord) finalizeDownstreamBody(body usageBody) usageBody {
	maxBytes := r.bodyOpts.effectiveMaxBytes()
	if maxBytes == 0 || body.Content == "" {
		return usageBody{}
	}
	if !r.bodyOpts.externalize {
		return truncateUsageBody(body.Content, maxBytes)
	}
	var value interface{}
	if err := json.Unmarshal([]byte(body.Content), &value); err == nil {
		r.assets.extractFromValue(value)
		if sanitized, err := json.Marshal(value); err == nil {
			return truncateUsageBody(string(sanitized), maxBytes)
		}
	}
	// SSE 流：逐行 best-effort，仅解析 data: 前缀且含媒体标记的行。
	externalized := r.assets.extractFromSSE(body.Content)
	return truncateUsageBody(externalized, maxBytes)
}

// truncateUsageBody 按上限截断序列化后的文本。
func truncateUsageBody(content string, maxBytes int) usageBody {
	if len(content) <= maxBytes {
		return usageBody{Content: content}
	}
	return usageBody{Content: content[:maxBytes], Truncated: true}
}

// appendStreamEvent 登记一条上游流事件：环形保留最后 StreamEventsCacheMax 条
// （终态事件——最终 usage、finish 原因——在流尾部，保尾不保头），非法 JSON
// 直接丢弃（坏片段混进数组会让整个事件数组的序列化永远失败）。
// 序列化推迟到 recordUsage 一次性物化：旧实现每事件重编组整个数组并完整
// 清洗，CPU 随事件数平方增长。
func (r *usageRecord) appendStreamEvent(payload string) {
	if !json.Valid([]byte(payload)) {
		return
	}
	if len(r.pendingStreamEvents) >= StreamEventsCacheMax {
		r.pendingStreamEvents = append(r.pendingStreamEvents[:0], r.pendingStreamEvents[1:]...)
	}
	r.pendingStreamEvents = append(r.pendingStreamEvents, json.RawMessage(payload))
}

// materializeStreamEvents 把捕获的流事件物化为 ProviderResponse（该记录
// 未显式赋值过时）。非流式路径不受影响。
func (r *usageRecord) materializeStreamEvents() {
	if r.ProviderResponse.Content != "" || len(r.pendingStreamEvents) == 0 {
		return
	}
	if eventBytes, err := json.Marshal(r.pendingStreamEvents); err == nil {
		r.ProviderResponse = r.sanitizeBody(eventBytes)
	}
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
	// Claude 的顶层 usage 键是上面前置探测未覆盖的最后一处；message/message_delta/
	// usageMetadata 已在前置探测处理，平台分支不再重复。
	if platform == relay.PlatformAnthropic {
		if raw, ok := payload["usage"].(map[string]interface{}); ok {
			return usageResultFromClaudeUsage(raw, source)
		}
	}
	switch platform {
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
	// completion/output 两个键是同一明细的两种命名（Responses 与 chat 兼容上游各用其一）。
	for _, key := range []string{"completion_tokens_details", "output_tokens_details"} {
		if details, ok := raw[key].(map[string]interface{}); ok {
			setDetailInt(&detail.ReasoningTokens, details, "reasoning_tokens")
			setDetailInt(&detail.TextOutputTokens, details, "text_tokens")
			setDetailInt(&detail.AudioOutputTokens, details, "audio_tokens")
			setDetailInt(&detail.ImageOutputTokens, details, "image_tokens")
		}
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
	// 物化后统一走 finalize（外置 + 最终截断）。
	if record.downstream != nil && record.DownstreamResponse.Content == "" {
		record.DownstreamResponse = record.finalizeDownstreamBody(record.downstream.downstreamBody())
	}
	// 流式路径的 ProviderResponse 在此一次性物化（捕获期只登记事件）。
	record.materializeStreamEvents()
	// 日志持久化总开关（usageLog.persistEnabled，默认 true）：关闭后完全不落库。
	if !s.usageLogConfig().PersistEnabled {
		return
	}
	if record.EndedAt.IsZero() {
		record.EndedAt = time.Now()
	}
	if record.DurationMs == 0 {
		record.DurationMs = record.EndedAt.Sub(record.StartedAt).Milliseconds()
	}

	if s.store == nil {
		// 无 store 的降级模式不再留存 usage（遗留面板已下线，无任何读取方）。
		return
	}
	record.writeGen = s.usageWriteGen.Load()
	// 优先异步落库，避免请求路径阻塞在 SQLite 写入上；
	// 队列满或未启动时降级为同步写，保证不丢记录。
	if s.enqueueUsageRecord(record) {
		return
	}
	s.persistUsageRecord(record)
}

// resetUsage 清空全部 usage 数据并失效响应缓存。仅由 admin 端点调用；
// 面板下线后不再有无 store 的内存态分支。
func (s *Server) resetUsage(c *gin.Context) {
	if s.store == nil {
		respondFail(c, http.StatusServiceUnavailable, "store_unavailable", "sqlite store is unavailable")
		return
	}
	// 先挡住 enqueue（w.mu），再拿 persist 锁清库/切 generation。
	// 顺序必须是 writer mu → persist mu：stopUsageWriter 持 writer mu 后 Wait
	// writer，而 writer 落库要 persist mu；若此处反序会与 stop 死锁。
	w := s.usageWriterSnapshot()
	if w != nil {
		w.mu.Lock()
	}
	s.usagePersistMu.Lock()
	if err := s.store.ClearUsage(c.Request.Context()); err != nil {
		s.usagePersistMu.Unlock()
		if w != nil {
			w.mu.Unlock()
		}
		// 回填进行中：绝不在此排队等锁（调用方已持有 writer/persist 锁，
		// 排队会卡住全部请求的 usage 落库），转 409 让用户稍后重试。
		if errors.Is(err, storage.ErrRollupBackfillInProgress) {
			respondFail(c, http.StatusConflict, "backfill_in_progress",
				"rollup backfill is running; retry after it completes")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 清库成功后，在 enqueue 仍被挡住时排空旧队列并递增 generation，
	// 避免「先加 generation 再 drain」把 reset 之后、drain 之前入队的新记录丢掉。
	s.drainUsageQueueFrom(w)
	s.usageWriteGen.Add(1)
	// 记录已全清：外置媒体资产目录一并清空。必须在 persist/write 锁释放前
	// 执行——之后放行的新请求会重建自己的资产目录，不会误删。
	if root := s.usageAssetsRoot(); root != "" {
		if err := os.RemoveAll(root); err != nil {
			log.Printf("usage reset: failed to remove assets root %s: %v", root, err)
		}
	}
	s.usagePersistMu.Unlock()
	if w != nil {
		w.mu.Unlock()
	}
	s.usageCache.flush()
	s.usageSeq.Add(1)
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

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// observingStreamWriter 是下游观察者：包裹写回客户端的流式 writer，仅负责
// 首字节计时与输出文本累积（本地 token 估算用）。事件捕获与 usage 提取由
// 上游观察者（upstreamUsageObservingBody）承担——若两者都写 ProviderResponse，
// transform 模式下最终值取决于读写交错且记录的是下游渲染格式而非上游原文。
type observingStreamWriter struct {
	inner        relay.StreamResponseWriter
	record       *usageRecord
	startTime    time.Time
	responseText strings.Builder
	lines        sseLineSplitter
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
	// SSE 以空行分帧，Flush 时行必完整；冲刷残余缓冲防止最后一行丢失。
	w.lines.flushRemainder()
	return w.inner.Flush()
}

func (w *observingStreamWriter) observe(data []byte) {
	if w.record == nil {
		return
	}
	if w.record.FirstByteMs == 0 && len(strings.TrimSpace(string(data))) > 0 {
		w.record.FirstByteMs = time.Since(w.startTime).Milliseconds()
	}
	// 行缓冲：data: 载荷可能跨多次 Write 到达，按单次调用切行会把半截 JSON
	// 当完整事件处理（详见 sseLineSplitter 注释）。
	w.lines.onLine = w.observeLine
	w.lines.feed(data)
}

func (w *observingStreamWriter) observeLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	w.responseText.WriteString(extractOutputTextFromStreamPayload(payload))
}

// sseLineSplitter 缓冲跨 Read/Write 到达的字节，按完整行回调 onLine。
// 观察者逐行解析 SSE，若直接对每次到达的字节片段 Split("\n")，一个跨两次
// Write 的 data: 载荷会被当成两条（半截）事件处理——坏 JSON 混进事件数组
// 后，json.Marshal 对内嵌 RawMessage 的校验会让之后的所有序列化全部失败。
type sseLineSplitter struct {
	buffer []byte
	onLine func(line string)
}

func (s *sseLineSplitter) feed(data []byte) {
	s.buffer = append(s.buffer, data...)
	for {
		idx := bytes.IndexByte(s.buffer, '\n')
		if idx < 0 {
			return
		}
		line := strings.TrimSpace(string(s.buffer[:idx]))
		s.buffer = s.buffer[idx+1:]
		s.onLine(line)
	}
}

func (s *sseLineSplitter) flushRemainder() {
	if line := strings.TrimSpace(string(s.buffer)); line != "" {
		s.buffer = nil
		s.onLine(line)
	}
	s.buffer = nil
}

type upstreamUsageObservingBody struct {
	inner    io.ReadCloser
	record   *usageRecord
	platform relay.Platform
	format   relay.FormatType
	lines    sseLineSplitter
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
	b.lines.flushRemainder()
	return b.inner.Close()
}

func (b *upstreamUsageObservingBody) observe(data []byte) {
	b.lines.onLine = b.observeLine
	b.lines.feed(data)
}

// observeLine 是 ProviderResponse 流事件与 usage 增量的唯一来源（上游线格式）。
func (b *upstreamUsageObservingBody) observeLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	b.record.appendStreamEvent(payload)
	result := extractProviderUsageFromStreamEvent(b.platform, b.format, payload)
	applyProviderUsageToRecord(b.record, result)
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
		charsPerToken = DefaultCharsPerToken
	}
	chars := len([]rune(text))
	if chars == 0 {
		return 0
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

func extractOutputTextFromMaheshvaraResponse(resp *relay.MaheshvaraResponse) string {
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
	record.SourceID = model.SourceID
	record.Platform = model.Platform
	record.TargetPlatform = string(platform)
}
