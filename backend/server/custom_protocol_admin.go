package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// 协议设计器（WebUI /protocols）的管理端点：协议持久化在 SQLite（写入即
// 同步注册表，热生效），预览/测试直接复用 relay 的渲染与映射实现，确保
// 设计器看到的行径与线上中转完全相同。

const (
	customProtocolMaxAdminBodyBytes = 8 << 20  // 管理端点请求体上限（含样例请求/文档附件）
	customProtocolTestBodyLimit     = 4 << 20  // 测试响应体读取上限
	customProtocolTestBodyEcho      = 64 << 10 // 原始响应回显上限（超出截断）
	customProtocolTestMaxEvents     = 50       // 流式测试最多采样的事件数
	customProtocolTestTimeoutSec    = 120      // 测试请求硬性总超时（含流式）
)

type customProtocolSummary struct {
	ID        string          `json:"id"`
	Name      string          `json:"name,omitempty"`
	Version   string          `json:"version,omitempty"`
	Type      string          `json:"type"`
	Valid     bool            `json:"valid"`
	Error     string          `json:"error,omitempty"`
	Config    json.RawMessage `json:"config"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

func (s *Server) adminListCustomProtocols(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	rows, err := store.ListCustomProtocols(c.Request.Context())
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	items := make([]customProtocolSummary, 0, len(rows))
	for _, row := range rows {
		summary := customProtocolSummary{
			ID:      row.ID,
			Name:    row.Name,
			Version: row.Version,
			Type:    row.Type,
			Config:  json.RawMessage(row.Config),
		}
		if !row.UpdatedAt.IsZero() {
			summary.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
		}
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal([]byte(row.Config), &protocol); err != nil {
			summary.Error = fmt.Sprintf("JSON 解析失败: %v", err)
		} else if err := relay.ValidateCustomProtocol(protocol); err != nil {
			summary.Error = err.Error()
		} else {
			summary.Valid = true
		}
		if normalized := relay.NormalizeCustomProtocolType(summary.Type); normalized != "" {
			summary.Type = normalized
		}
		items = append(items, summary)
	}
	respondOK(c, gin.H{"items": items})
}

// adminCustomProtocolSchema 返回字段目录与约束：UI 下拉、AI harness 提示词
// 与后端校验共用的单一事实来源。
func (s *Server) adminCustomProtocolSchema(c *gin.Context) {
	respondOK(c, relay.CustomProtocolSchemaFor())
}

// validateCustomProtocolRaw 解析并校验一段协议 JSON，失败时已向客户端回错。
func validateCustomProtocolRaw(c *gin.Context, raw json.RawMessage) (relay.CustomProtocolConfig, bool) {
	var protocol relay.CustomProtocolConfig
	if len(raw) == 0 {
		respondFail(c, http.StatusBadRequest, "invalid_protocol", "缺少 protocol 字段")
		return relay.CustomProtocolConfig{}, false
	}
	if err := json.Unmarshal(raw, &protocol); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_protocol", fmt.Sprintf("协议结构解析失败: %v", err))
		return relay.CustomProtocolConfig{}, false
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_protocol", err.Error())
		return relay.CustomProtocolConfig{}, false
	}
	return protocol, true
}

func (s *Server) adminUpsertCustomProtocol(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	pathID := strings.TrimSpace(c.Param("id"))
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, customProtocolMaxAdminBodyBytes))
	if err != nil {
		respondFail(c, http.StatusBadRequest, "read_body_failed", err.Error())
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", fmt.Sprintf("协议必须是合法 JSON: %v", err))
		return
	}
	protocol, ok := validateCustomProtocolRaw(c, compact.Bytes())
	if !ok {
		return
	}
	if strings.ToLower(strings.TrimSpace(protocol.ID)) != strings.ToLower(pathID) {
		respondFail(c, http.StatusBadRequest, "id_mismatch", "请求体中的协议 id 与路径参数不一致")
		return
	}

	// 存原始 JSON 而非重新序列化的结构体：保留未知字段与用户排版，向前兼容。
	if err := store.UpsertCustomProtocol(c.Request.Context(), storage.CustomProtocol{
		ID:      protocol.ID,
		Name:    protocol.Name,
		Version: protocol.Version,
		Type:    relay.NormalizeCustomProtocolType(protocol.Type),
		Config:  compact.String(),
	}); err != nil {
		respondFail(c, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	syncErr := s.syncCustomProtocolsQuiet()
	response := gin.H{"saved": true, "id": pathID, "valid": true, "synced": syncErr == nil}
	if syncErr != nil {
		response["warning"] = fmt.Sprintf("已保存，但注册表同步失败（运行中的旧协议保持不变）: %v", syncErr)
	}
	respondOK(c, response)
}

func (s *Server) adminDeleteCustomProtocol(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	pathID := strings.ToLower(strings.TrimSpace(c.Param("id")))
	deleted, err := store.DeleteCustomProtocol(c.Request.Context(), pathID)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	if !deleted {
		respondFail(c, http.StatusNotFound, "not_found", fmt.Sprintf("协议 %q 不存在", pathID))
		return
	}
	syncErr := s.syncCustomProtocolsQuiet()
	respondOK(c, gin.H{"deleted": true, "synced": syncErr == nil})
}

// syncCustomProtocolsQuiet 从 SQLite 读取协议并原子替换注册表；失败以 error
// 返回——管理端点需要把"已保存但未生效"如实回传给前端。
func (s *Server) syncCustomProtocolsQuiet() error {
	if s.store == nil {
		return fmt.Errorf("sqlite store is unavailable")
	}
	rows, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		return err
	}
	configs := make([]relay.CustomProtocolConfig, 0, len(rows))
	for _, row := range rows {
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal([]byte(row.Config), &protocol); err != nil {
			return fmt.Errorf("custom protocol %q is invalid JSON: %w", row.ID, err)
		}
		configs = append(configs, protocol)
	}
	return relay.ReplaceCustomProtocols(configs)
}

// migrateLegacyCustomProtocols 把 config.json 中已废弃的 customProtocols 键
// 一次性导入 SQLite（同 ID 已存在时以库为准），键随即从文件移除。
func (s *Server) migrateLegacyCustomProtocols() {
	if s.store == nil {
		return
	}
	entries := s.config.TakeDeprecatedCustomProtocols()
	if len(entries) == 0 {
		return
	}
	existing, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		log.Printf("custom protocol migration aborted: %v", err)
		return
	}
	known := make(map[string]bool, len(existing))
	for _, row := range existing {
		known[strings.ToLower(row.ID)] = true
	}
	imported := 0
	for _, raw := range entries {
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal(raw, &protocol); err != nil {
			log.Printf("custom protocol migration skipped an invalid entry: %v", err)
			continue
		}
		id := strings.ToLower(strings.TrimSpace(protocol.ID))
		if id == "" || known[id] {
			continue
		}
		if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(protocol, string(raw))); err != nil {
			log.Printf("custom protocol migration failed for %q: %v", protocol.ID, err)
			continue
		}
		imported++
	}
	if imported > 0 {
		log.Printf("migrated %d custom protocol(s) from config.json into the database", imported)
	}
}

type customProtocolPreviewPayload struct {
	Protocol      json.RawMessage          `json:"protocol"`
	SampleRequest *relay.MaheshvaraRequest `json:"sampleRequest,omitempty"`
}

type customProtocolPreviewResult struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Query       map[string]string `json:"query,omitempty"`
	Headers     map[string]string `json:"headers"`
	ContentType string            `json:"contentType"`
	Body        string            `json:"body,omitempty"`
	// AuthPreview 描述真实发送时的凭证注入方式；注入的具体值恒为占位符，
	// 不泄露任何密钥。
	AuthPreview string `json:"authPreview"`
}

// defaultCustomProtocolSampleRequest 是设计器预览/测试的默认样例：覆盖常用
// 占位符（messages/生成参数/stream），让用户无需先构造请求即可看到渲染效果。
func defaultCustomProtocolSampleRequest() *relay.MaheshvaraRequest {
	temperature := 0.7
	maxTokens := 1024
	return &relay.MaheshvaraRequest{
		Model: "sample-model",
		Messages: []relay.MaheshvaraMessage{
			{Role: "system", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "You are a helpful assistant."}}},
			{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "Hello!"}}},
		},
		Temperature:     &temperature,
		MaxOutputTokens: maxTokens,
		Stream:          false,
	}
}

func (s *Server) adminPreviewCustomProtocol(c *gin.Context) {
	var payload customProtocolPreviewPayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	protocol, ok := validateCustomProtocolRaw(c, payload.Protocol)
	if !ok {
		return
	}
	sample := payload.SampleRequest
	if sample == nil {
		sample = defaultCustomProtocolSampleRequest()
	}
	result, err := previewCustomProtocolRequest(protocol, sample)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "render_failed", err.Error())
		return
	}
	respondOK(c, result)
}

// previewCustomProtocolRequest 用样例请求渲染协议，并模拟 auth 注入形态。
func previewCustomProtocolRequest(protocol relay.CustomProtocolConfig, sample *relay.MaheshvaraRequest) (customProtocolPreviewResult, error) {
	rendered, err := relay.RenderCustomProtocolRequest(sample, protocol)
	if err != nil {
		return customProtocolPreviewResult{}, err
	}
	headers := make(map[string]string, len(rendered.Headers)+1)
	for key, value := range rendered.Headers {
		headers[key] = value
	}
	body := ""
	if len(rendered.Body) > 0 {
		var indented bytes.Buffer
		if json.Indent(&indented, rendered.Body, "", "  ") == nil {
			body = indented.String()
		} else {
			body = string(rendered.Body)
		}
	}
	authMode := strings.ToLower(strings.TrimSpace(rendered.Auth.Mode))
	if authMode == "" {
		authMode = "bearer"
	}
	authPreview := "不注入 API key"
	switch authMode {
	case "bearer":
		headers["Authorization"] = "Bearer <source-api-key>"
		authPreview = "Authorization: Bearer <模型源 API key>"
	case "header":
		headerName := rendered.Auth.Header
		if strings.TrimSpace(headerName) == "" {
			headerName = "x-api-key"
		}
		headers[headerName] = rendered.Auth.Prefix + "<source-api-key>"
		authPreview = fmt.Sprintf("%s: %s<模型源 API key>", headerName, rendered.Auth.Prefix)
	case "query":
		queryKey := rendered.Auth.Query
		if strings.TrimSpace(queryKey) == "" {
			queryKey = "api_key"
		}
		if rendered.Query == nil {
			rendered.Query = map[string]string{}
		}
		rendered.Query[queryKey] = "<source-api-key>"
		authPreview = fmt.Sprintf("query 参数 %s=<模型源 API key>", queryKey)
	}
	return customProtocolPreviewResult{
		Method:      rendered.Method,
		Path:        rendered.Path,
		Query:       rendered.Query,
		Headers:     headers,
		ContentType: rendered.ContentType,
		Body:        body,
		AuthPreview: authPreview,
	}, nil
}

type customProtocolTestPayload struct {
	Protocol      json.RawMessage          `json:"protocol"`
	SourceID      string                   `json:"sourceId"`
	Model         string                   `json:"model"`
	BaseURL       string                   `json:"baseUrl,omitempty"`
	APIKey        string                   `json:"apiKey,omitempty"`
	Stream        bool                     `json:"stream"`
	SampleRequest *relay.MaheshvaraRequest `json:"sampleRequest,omitempty"`
}

type customProtocolModelsTestPayload struct {
	Protocol json.RawMessage `json:"protocol"`
	BaseURL  string          `json:"baseUrl"`
	APIKey   string          `json:"apiKey,omitempty"`
}

type customProtocolStreamSample struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

// adminTestCustomProtocol 向所选模型源的上游真实发送一次渲染后的请求（用户
// 在设计器中显式触发），返回上游原文与映射出的 Maheshvara 结果供对照。
func (s *Server) adminTestCustomProtocol(c *gin.Context) {
	var payload customProtocolTestPayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	protocol, ok := validateCustomProtocolRaw(c, payload.Protocol)
	if !ok {
		return
	}
	// 两种凭据来源：临时输入（baseUrl 直连，适合协议尚未落源时调试）或已保存
	// 模型源 + 模型行（走入库快照的 baseUrl/key）。临时模式不触碰库,store
	// 仅在源模式需要。
	adHoc := strings.TrimSpace(payload.BaseURL) != ""
	modelName := strings.TrimSpace(payload.Model)
	if modelName == "" || (!adHoc && strings.TrimSpace(payload.SourceID) == "") {
		respondFail(c, http.StatusBadRequest, "missing_target", "必须指定测试使用的模型源与模型，或填入临时 baseUrl 与模型名")
		return
	}
	var baseURL, apiKey string
	if adHoc {
		baseURL = strings.TrimSpace(payload.BaseURL)
		apiKey = payload.APIKey
	} else {
		store, okStore := s.requireStore(c)
		if !okStore {
			return
		}
		model, found := findCustomProtocolTestModel(c.Request.Context(), store, payload.SourceID, payload.Model)
		if !found {
			respondFail(c, http.StatusNotFound, "model_not_found",
				fmt.Sprintf("模型源 %q 下没有找到模型 %q", payload.SourceID, payload.Model))
			return
		}
		baseURL, apiKey, modelName = model.BaseURL, model.APIKey, model.Name
	}

	sample := payload.SampleRequest
	if sample == nil {
		sample = defaultCustomProtocolSampleRequest()
	}
	sample.Model = modelName
	sample.Stream = payload.Stream

	rendered, err := relay.RenderCustomProtocolRequest(sample, protocol)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "render_failed", err.Error())
		return
	}

	timeout := s.probeTimeout(customProtocolTestTimeoutSec * time.Second)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	started := time.Now()
	response, err := s.openaiAdapter.SendCustomProtocolRequest(ctx, baseURL, apiKey, rendered, payload.Stream)
	if err != nil {
		respondFail(c, http.StatusBadGateway, "send_failed", err.Error())
		return
	}
	defer response.Body.Close()
	result := gin.H{
		"statusCode":  response.StatusCode,
		"durationMs":  time.Since(started).Milliseconds(),
		"targetModel": modelName,
		"stream":      payload.Stream,
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, customProtocolTestBodyLimit))
		result["rawBody"] = truncateForDisplay(string(raw), customProtocolTestBodyEcho)
		respondOK(c, result)
		return
	}
	if payload.Stream {
		events, decoded, streamErr := sampleCustomProtocolStream(ctx, protocol, response.Body)
		result["events"] = events
		result["decoded"] = decoded
		if streamErr != nil {
			result["streamError"] = streamErr.Error()
		}
		respondOK(c, result)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, customProtocolTestBodyLimit))
	result["rawBody"] = truncateForDisplay(string(raw), customProtocolTestBodyEcho)
	mapped, mappingErr := relay.CustomProtocolResponseToMaheshvara(raw, protocol)
	if mappingErr != nil {
		result["mappingError"] = mappingErr.Error()
	} else if encoded, err := json.MarshalIndent(mapped, "", "  "); err == nil {
		result["maheshvara"] = json.RawMessage(encoded)
	}
	respondOK(c, result)
}

// sampleCustomProtocolStream 读取流式测试的上游响应：采样前 N 个原始 SSE 事件
// 及其解码出的 Maheshvara 流事件，供设计器对照"上游原文 → 映射结果"。
// errStreamSampleCapReached 让 ForEachBatch 干净收止(采样上限到达,非流错误)。
var errStreamSampleCapReached = errors.New("stream sample cap reached")

// customProtocolTestEventEchoBytes 是采样事件原文的展示截断上限。
const customProtocolTestEventEchoBytes = 4096

func sampleCustomProtocolStream(ctx context.Context, protocol relay.CustomProtocolConfig, body io.ReadCloser) ([]customProtocolStreamSample, []json.RawMessage, error) {
	decoder, err := relay.NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		return nil, nil, err
	}
	reader := relay.NewSSEEventReader(body)
	defer reader.Close()
	var events []customProtocolStreamSample
	var decoded []json.RawMessage
	// 排水语义与转发路径共用 ForEachBatch;采样上限到达即以哨兵错误干净收止。
	streamErr := decoder.ForEachBatch(ctx, reader, func(wire relay.SSEEvent, maheshvaraEvents []relay.MaheshvaraStreamEvent, terminalBeforeBatch bool) error {
		if len(events) >= customProtocolTestMaxEvents {
			return errStreamSampleCapReached
		}
		events = append(events, customProtocolStreamSample{Event: wire.Event, Data: truncateForDisplay(wire.Data, customProtocolTestEventEchoBytes)})
		for _, event := range maheshvaraEvents {
			if terminalBeforeBatch && event.Usage == nil && event.Error == nil {
				continue
			}
			if encoded, err := json.Marshal(event); err == nil {
				decoded = append(decoded, encoded)
			}
		}
		return nil
	})
	if errors.Is(streamErr, errStreamSampleCapReached) {
		streamErr = nil
	}
	if streamErr == nil && !decoder.TerminalReceived() {
		streamErr = fmt.Errorf("流结束前未收到配置的终止标记（doneValues/finish reason）")
	}
	return events, decoded, streamErr
}

// adminTestCustomProtocolModels 用临时凭据试拉模型列表：按协议 models 发现
// 配置请求上游，返回发现的模型与原文供设计器对照，不写库——「一系列测试」的
// 模型发现环节，也免去为试协议先建源。
func (s *Server) adminTestCustomProtocolModels(c *gin.Context) {
	var payload customProtocolModelsTestPayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	protocol, ok := validateCustomProtocolRaw(c, payload.Protocol)
	if !ok {
		return
	}
	baseURL := strings.TrimSpace(payload.BaseURL)
	if baseURL == "" {
		respondFail(c, http.StatusBadRequest, "missing_target", "需要填写 baseUrl")
		return
	}
	if protocol.Models == nil {
		respondFail(c, http.StatusBadRequest, "no_discovery", "协议未声明模型发现配置（models.path / models.listPath）")
		return
	}
	rendered, err := relay.RenderCustomProtocolModelsRequest(protocol)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "render_failed", err.Error())
		return
	}

	timeout := s.probeTimeout(customProtocolTestTimeoutSec * time.Second)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	started := time.Now()
	response, err := s.openaiAdapter.SendCustomProtocolRequest(ctx, baseURL, payload.APIKey, rendered, false)
	if err != nil {
		respondFail(c, http.StatusBadGateway, "send_failed", err.Error())
		return
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, customProtocolTestBodyLimit))
	result := gin.H{
		"statusCode": response.StatusCode,
		"durationMs": time.Since(started).Milliseconds(),
		"rawBody":    truncateForDisplay(string(raw), customProtocolTestBodyEcho),
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		infos, parseErr := relay.ParseCustomProtocolModels(raw, protocol)
		if parseErr != nil {
			result["parseError"] = parseErr.Error()
		} else {
			models := make([]gin.H, 0, len(infos))
			for _, info := range infos {
				models = append(models, gin.H{"id": info.ID, "name": info.Name})
			}
			result["models"] = models
		}
	}
	respondOK(c, result)
}

func findCustomProtocolTestModel(ctx context.Context, store *storage.Store, sourceID, modelName string) (storage.Model, bool) {
	models, err := store.ListModelsFiltered(ctx, storage.ModelListFilter{SourceID: strings.TrimSpace(sourceID)})
	if err != nil {
		return storage.Model{}, false
	}
	modelName = strings.TrimSpace(modelName)
	for _, model := range models {
		if model.Name == modelName || model.ID == modelName {
			return model, true
		}
	}
	for _, model := range models {
		if strings.EqualFold(model.Name, modelName) || strings.EqualFold(model.ID, modelName) {
			return model, true
		}
	}
	return storage.Model{}, false
}

func bindAdminJSON(c *gin.Context, target any) error {
	defer c.Request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, customProtocolMaxAdminBodyBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// probeTimeout 计算探活类请求(真实测试/试拉/助手)的超时:自定义基础值,
// 被更短的 HTTP 全局超时钳制。
func (s *Server) probeTimeout(base time.Duration) time.Duration {
	if seconds := s.config.GetHTTPTimeout(); seconds > 0 && time.Duration(seconds)*time.Second < base {
		return time.Duration(seconds) * time.Second
	}
	return base
}

func truncateForDisplay(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// 按字节截断可能劈开多字节字符：回退到最近的 rune 边界再切。
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + fmt.Sprintf("\n…（已截断，共 %d 字节）", len(value))
}
