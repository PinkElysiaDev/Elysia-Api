package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

const usageBodyMaxBytes = 64 * 1024

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
	LastUsedAt                   time.Time `json:"lastUsedAt,omitempty"`
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
		RequestID:    fmt.Sprintf("req_%d", start.UnixNano()),
		StartedAt:    start,
		KeyName:      c.GetString("elysiaKeyName"),
		KeyHash:      c.GetString("elysiaKeyHash"),
		InputFormat:  string(inputFormat),
		StatusCode:   http.StatusOK,
		IncomingBody: sanitizeUsageBody(body),
	}
}

func sanitizeUsageBody(data []byte) usageBody {
	truncated := len(data) > usageBodyMaxBytes
	if truncated {
		data = data[:usageBodyMaxBytes]
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

func usageFromProviderBody(platform relay.Platform, body []byte) usageTokenUsage {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return usageTokenUsage{}
	}

	switch platform {
	case relay.PlatformAnthropic:
		if raw, ok := payload["usage"].(map[string]interface{}); ok {
			return usageFromClaudeUsage(raw)
		}
	case relay.PlatformGemini:
		if raw, ok := payload["usageMetadata"].(map[string]interface{}); ok {
			return usageFromGeminiUsageMetadata(raw)
		}
	default:
		usage := usageFromOpenAICompatiblePayload(payload)
		if usageHasAnyTokens(usage) {
			return usage
		}
	}
	return usageTokenUsage{}
}

func usageFromOpenAICompatiblePayload(payload map[string]interface{}) usageTokenUsage {
	usage := usageTokenUsage{}
	if raw, ok := payload["usage"].(map[string]interface{}); ok {
		usage = usageFromOpenAIUsage(raw)
	}

	cacheHitTokens := getInt(usage.CacheHitTokens)
	cacheFieldSeen := usage.CacheHitTokens != nil
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
		usage.CacheHitTokens = intPtr(cacheHitTokens)
	}
	return usage
}

func usageHasAnyTokens(usage usageTokenUsage) bool {
	return usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil || usage.CacheHitTokens != nil
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
	if record.EndedAt.IsZero() {
		record.EndedAt = time.Now()
	}
	if record.DurationMs == 0 {
		record.DurationMs = record.EndedAt.Sub(record.StartedAt).Milliseconds()
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
	for _, record := range s.usageSnapshot() {
		if record.RequestID == id {
			c.JSON(http.StatusOK, record)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "usage log not found"})
}

func (s *Server) resetUsage(c *gin.Context) {
	if err := s.clearUsageRecords(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

func usageTimeRange(c *gin.Context) (time.Time, time.Time) {
	to := time.Now()
	from := to.Add(-24 * time.Hour)
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
		if record.StartedAt.Before(from) || record.StartedAt.After(to) {
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
	if record.StartedAt.After(summary.LastUsedAt) {
		summary.LastUsedAt = record.StartedAt
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
	start := truncateUsageWindow(from.In(location), window)
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
	switch window {
	case "5m":
		return t.Truncate(5 * time.Minute)
	case "15m":
		return t.Truncate(15 * time.Minute)
	case "minute":
		return t.Truncate(time.Minute)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	default:
		return t.Truncate(time.Hour)
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
	if !w.observeUsage {
		return
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
		if len(w.events) < 50 {
			w.events = append(w.events, json.RawMessage(payload))
			if eventBytes, err := json.Marshal(w.events); err == nil {
				w.record.ProviderResponse = sanitizeUsageBody(eventBytes)
			}
		}
		usage, ok := parseStreamUsage(payload)
		if ok {
			w.record.Usage = mergeUsage(w.record.Usage, usage)
		}
	}
}

type upstreamUsageObservingBody struct {
	inner    io.ReadCloser
	record   *usageRecord
	platform relay.Platform
	buffer   []byte
	events   []json.RawMessage
}

func observeUpstreamUsage(resp *http.Response, record *usageRecord, platform relay.Platform) {
	if resp == nil || resp.Body == nil || record == nil {
		return
	}
	resp.Body = &upstreamUsageObservingBody{inner: resp.Body, record: record, platform: platform}
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
	usage, ok := parsePlatformStreamUsage(b.platform, payload)
	if ok {
		b.record.Usage = mergeUsage(b.record.Usage, usage)
	}
}

func parsePlatformStreamUsage(platform relay.Platform, payload string) (usageTokenUsage, bool) {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return usageTokenUsage{}, false
	}
	switch platform {
	case relay.PlatformAnthropic:
		if raw, ok := event["message"].(map[string]interface{}); ok {
			if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
				return usageFromClaudeUsage(usageRaw), true
			}
		}
		if usageRaw, ok := event["usage"].(map[string]interface{}); ok {
			return usageFromClaudeUsage(usageRaw), true
		}
	case relay.PlatformGemini:
		if raw, ok := event["usageMetadata"].(map[string]interface{}); ok {
			return usageFromGeminiUsageMetadata(raw), true
		}
	default:
		if raw, ok := event["usage"].(map[string]interface{}); ok {
			return usageFromOpenAIUsage(raw), true
		}
	}
	return usageTokenUsage{}, false
}

func parseStreamUsage(payload string) (usageTokenUsage, bool) {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return usageTokenUsage{}, false
	}
	if raw, ok := event["usageMetadata"].(map[string]interface{}); ok {
		return usageFromGeminiUsageMetadata(raw), true
	}
	if raw, ok := event["message"].(map[string]interface{}); ok {
		if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
			return usageFromClaudeUsage(usageRaw), true
		}
	}
	if raw, ok := event["message_delta"].(map[string]interface{}); ok {
		if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
			return usageFromClaudeUsage(usageRaw), true
		}
	}
	if raw, ok := event["usage"].(map[string]interface{}); ok {
		return usageFromOpenAIUsage(raw), true
	}
	return usageTokenUsage{}, false
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
