package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type usageBody struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type usageTokenUsage struct {
	InputTokens  *int `json:"inputTokens,omitempty"`
	OutputTokens *int `json:"outputTokens,omitempty"`
	TotalTokens  *int `json:"totalTokens,omitempty"`
	CacheHitTokens *int `json:"cacheHitTokens,omitempty"`
	// CacheCreationTokens 是缓存创建 token 数。指针语义保留「上游未上报」
	// （nil）与「上报为零」（指向 0）之别，落库时据此置位 UsageReportMask。
	CacheCreationTokens *int `json:"cacheCreationTokens,omitempty"`
	EstimatedTokens     int  `json:"estimatedTokens,omitempty"`
	Estimated           bool `json:"estimated,omitempty"`
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
	hostedTools         *builtin.ToolAccounting
	IngressRevision     string                     `json:"ingressRevision,omitempty"`
	UpstreamRevision    string                     `json:"upstreamRevision,omitempty"`
	ProtocolUsage       *protocol.Usage            `json:"protocolUsage,omitempty"`
	ProtocolResponseID  string                     `json:"protocolResponseId,omitempty"`
	ConversionIssues    []protocol.ConversionIssue `json:"conversionIssues,omitempty"`
	RequestID           string                     `json:"requestId"`
	StartedAt           time.Time                  `json:"startedAt"`
	EndedAt             time.Time                  `json:"endedAt"`
	KeyName             string                     `json:"keyName"`
	KeyHash             string                     `json:"keyHash"`
	RequestedModelGroup string                     `json:"requestedModelGroup"`
	GroupID             string                     `json:"groupId"`
	GroupName           string                     `json:"groupName"`
	ModelID             string                     `json:"modelId"`
	ModelName           string                     `json:"modelName"`
	SourceID            string                     `json:"sourceId,omitempty"`
	Platform            string                     `json:"platform"`
	InputFormat         string                     `json:"inputFormat"`
	TargetPlatform      string                     `json:"targetPlatform"`
	SourceFormat        string                     `json:"sourceFormat,omitempty"`
	TargetFormat        string                     `json:"targetFormat,omitempty"`
	SourceEndpoint      string                     `json:"sourceEndpoint,omitempty"`
	TargetEndpoint      string                     `json:"targetEndpoint,omitempty"`
	RelayMode           string                     `json:"relayMode,omitempty"`
	ResponsesMode       string                     `json:"responsesMode,omitempty"`
	ConversionChain     []string                   `json:"conversionChain,omitempty"`
	UsageSource         string                     `json:"usageSource,omitempty"`
	RequestWarnings     []string                   `json:"requestWarnings,omitempty"`
	Stream              bool                       `json:"stream"`
	StatusCode          int                        `json:"statusCode"`
	Error               string                     `json:"error,omitempty"`
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

// usageBodyOptions snapshots capture policy once per request; zero disables capture.
type usageBodyOptions struct {
	maxBytes    int
	externalize bool
}

func shortTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

func (s *Server) initUsageRecord(c *gin.Context, start time.Time, body []byte, inputFormat builtin.FormatType) *usageRecord {
	cfg := s.usageLogConfig()
	requestID := usageRequestID(start)
	record := &usageRecord{
		RequestID:   requestID,
		StartedAt:   start,
		KeyName:     c.GetString("elysiaKeyName"),
		KeyHash:     c.GetString("elysiaKeyHash"),
		InputFormat: string(inputFormat),
		StatusCode:  http.StatusOK,
		bodyOpts:    usageBodyOptions{maxBytes: cfg.BodyMaxBytes, externalize: cfg.ExternalizeMedia},
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
	maxBytes := r.bodyOpts.maxBytes
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
	maxBytes := r.bodyOpts.maxBytes
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
	if r.bodyOpts.maxBytes == 0 || !json.Valid([]byte(payload)) {
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

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// reportMask 把「上游是否上报了该计数」编码为 storage.UsageReport* 位。指针
// 语义在此兑现：nil（上游未上报）不置位，非 nil（含显式零）置位。record.Usage
// 的 Input/CacheHit/CacheCreation 只会被 updateRecordProtocolUsage 从协议观测
// 结果赋值（估算路径只写 EstimatedTokens，不碰这些指针），且 MergeUsage 唯一
// 合成的 Inferred 计数是 Total（不参与掩码），因此「非 nil」等价于「上游确实
// 上报」。命中率的可比性依赖此位：未上报不得当作零参与聚合。
func reportMask(usage usageTokenUsage) int {
	mask := 0
	if usage.InputTokens != nil {
		mask |= storage.UsageReportInput
	}
	if usage.CacheHitTokens != nil {
		mask |= storage.UsageReportCacheHit
	}
	if usage.CacheCreationTokens != nil {
		mask |= storage.UsageReportCreation
	}
	return mask
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
		respondFail(c, http.StatusInternalServerError, "clear_usage_failed", err.Error())
		return
	}
	// 清库成功后，在 enqueue 仍被挡住时排空旧队列并递增 generation，
	// 避免「先加 generation 再 drain」把 reset 之后、drain 之前入队的新记录丢掉。
	s.drainUsageQueueFrom(w)
	s.usageWriteGen.Add(1)
	s.usagePersistMu.Unlock()
	if w != nil {
		w.mu.Unlock()
	}
	s.usageCache.flush()
	s.usageSeq.Add(1)
	s.logSystemEvent("warn", "usage statistics and logs reset", nil)
	// 与失败路径同用 admin 封套（respondFail），客户端按 ok 字段统一判读。
	reclaimQueued := false
	if s.usageRetention != nil {
		reclaimQueued = s.usageRetention.triggerAsync()
	}
	respondOK(c, gin.H{"reset": true, "reclaimQueued": reclaimQueued})
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
// 首字节计时。事件捕获与 usage 提取由
// 上游观察者（upstreamUsageObservingBody）承担——若两者都写 ProviderResponse，
// transform 模式下最终值取决于读写交错且记录的是下游渲染格式而非上游原文。
func sseDataPayload(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return "", false
	}
	return payload, true
}

// sseLineSplitter 缓冲跨 Read/Write 到达的字节，按完整行回调 onLine。
// 观察者逐行解析 SSE，若直接对每次到达的字节片段 Split("\n")，一个跨两次
// Write 的 data: 载荷会被当成两条（半截）事件处理——坏 JSON 混进事件数组
// 后，json.Marshal 对内嵌 RawMessage 的校验会让之后的所有序列化全部失败。
// 互斥保护：上游观察者的 Close（flushRemainder）与扫描 goroutine 解除
// 阻塞后的最后一次 feed 可能并发（inner.Close 先唤醒阻塞中的 Read）；
// 回调作为参数传入而非结构体字段，避免回调字段自身的读写竞争。
func setRecordGroup(record *usageRecord, group *config.ModelGroupConfig) {
	record.GroupID = group.ID
	record.GroupName = group.Name
}

func setRecordModel(record *usageRecord, model config.ModelRef, platform builtin.Platform) {
	record.ModelID = model.ID
	record.ModelName = model.Name
	record.SourceID = model.SourceID
	record.Platform = model.Platform
	record.TargetPlatform = string(platform)
}
