package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type adminError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func respondOK(c *gin.Context, data any) { c.JSON(http.StatusOK, gin.H{"ok": true, "data": data}) }

func respondFail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"ok": false, "error": adminError{Code: code, Message: message}})
}

func (s *Server) setupAdminRoutes(admin *gin.RouterGroup) {
	admin.GET("/runtime-config", s.adminRuntimeConfig)
	admin.PUT("/runtime-config", s.adminUpdateRuntimeConfig)
	admin.POST("/reload", s.adminReload)
	admin.POST("/restart-required/check", s.adminRestartRequired)
	admin.GET("/model-sources", s.adminListSources)
	admin.POST("/model-sources", s.adminUpsertSource)
	admin.PUT("/model-sources/:id", s.adminUpsertSource)
	admin.DELETE("/model-sources/:id", s.adminDeleteSource)
	admin.POST("/model-sources/:id/fetch", s.adminFetchSource)
	admin.PATCH("/model-sources/:id/enabled", s.adminSetSourceEnabled)
	admin.GET("/model-catalog/status", s.adminModelCatalogStatus)
	admin.POST("/model-catalog/refresh", s.adminModelCatalogRefresh)
	admin.GET("/models", s.adminListModels)
	admin.POST("/models/refresh", s.adminRefreshModels)
	// modelId 走 query 而非路径段：模型 ID 常含 "/"（如 org/model），路径参数
	// 在 Gin 路由前已被 URL 解码，%2F 会拆成多段导致 404；query 值只经一次
	// 解码、不参与路由匹配，对任意字符稳健。
	admin.PATCH("/models/:sourceId", s.adminUpdateModel)
	admin.DELETE("/models/:sourceId", s.adminDeleteModel)
	admin.GET("/model-groups", s.adminListGroups)
	admin.POST("/model-groups", s.adminUpsertGroup)
	admin.PUT("/model-groups/:id", s.adminUpsertGroup)
	admin.DELETE("/model-groups/:id", s.adminDeleteGroup)
	admin.POST("/model-groups/:id/models", s.adminAddGroupMembers)
	admin.DELETE("/model-groups/:id/models", s.adminRemoveGroupMembers)
	admin.GET("/api-tokens", s.adminListTokens)
	admin.GET("/api-tokens/:name/reveal", s.adminRevealToken)
	admin.POST("/api-tokens", s.adminUpsertToken)
	admin.PUT("/api-tokens/:name", s.adminUpsertToken)
	admin.DELETE("/api-tokens/:name", s.adminDeleteToken)
	admin.GET("/usage/stats", s.usageCache.middleware(), s.adminUsageStats)
	admin.GET("/usage/trend", s.usageCache.middleware(), s.adminUsageTrend)
	admin.GET("/usage/pulse", s.usageCache.middleware(), s.adminUsagePulse)
	admin.GET("/usage/by-model", s.usageCache.middleware(), s.adminUsageByModel)
	admin.GET("/usage/by-model-daily", s.usageCache.middleware(), s.adminUsageByModelDaily)
	admin.GET("/usage/logs", s.usageCache.middleware(), s.adminUsageLogs)
	admin.GET("/usage/logs/:id", s.adminUsageLogDetail)
	admin.GET("/usage/seq", s.adminUsageSeq)
	admin.POST("/usage/reset", s.adminUsageReset)
	// 日志管理：外置媒体文件、占用状态、手动触发清理巡检。
	admin.GET("/usage/assets/:requestId/:file", s.adminUsageAsset)
	admin.GET("/usage/storage", s.adminUsageStorage)
	admin.POST("/usage/cleanup", s.adminUsageCleanup)
	admin.GET("/logs", s.adminSystemLogs)
	admin.GET("/health", s.adminHealth)
}

func (s *Server) requireStore(c *gin.Context) (*storage.Store, bool) {
	if s.store == nil {
		respondFail(c, http.StatusServiceUnavailable, "store_unavailable", "sqlite store is unavailable")
		return nil, false
	}
	return s.store, true
}

func (s *Server) adminRuntimeConfig(c *gin.Context) {
	server := s.config.GetServer()
	catalog := s.config.GetModelCatalog()
	catalogEnabled := catalog.Enabled == nil || *catalog.Enabled
	usageLog := s.config.GetUsageLogConfig()
	respondOK(c, gin.H{
		"host":                server.Host,
		"port":                server.Port,
		"panelAccessToken":    s.config.GetPanelAccessToken(),
		"databasePath":        s.config.GetDatabasePath(),
		"defaultDatabasePath": s.config.GetDefaultDatabasePath(),
		"logLevel":            s.config.GetLogLevel(),
		"httpTimeout":         s.config.GetHTTPTimeout(),
		"enablePprof":         s.config.GetEnablePprof(),
		"allowFakeIPOutbound": s.config.IsFakeIPOutboundAllowed(),
		"usageLog": gin.H{
			// 生效值（归一化后）：表单直接显示当前实际口径，保存时整体回写。
			"persistEnabled":         usageLog.PersistEnabled,
			"retentionDays":          usageLog.RetentionDays,
			"maxStorageMB":           usageLog.MaxStorageBytes / 1024 / 1024,
			"maxRecords":             usageLog.MaxRecords,
			"bodyMaxKB":              usageLog.BodyMaxBytes / 1024,
			"bodyOnErrorOnly":        usageLog.BodyOnErrorOnly,
			"externalizeMedia":       usageLog.ExternalizeMedia,
			"cleanupIntervalMinutes": int(usageLog.CleanupInterval.Minutes()),
		},
		"modelCatalog": gin.H{
			"enabled": catalogEnabled,
			"url":     catalogResolveURL(catalog),
			"proxy":   catalog.Proxy,
			// 未配置（nil）时回填默认 1440 供表单显示；显式 0 = 不启用定期同步。
			"syncIntervalMinutes": config.ResolveModelCatalogInterval(catalog),
		},
	})
}

func (s *Server) adminUpdateRuntimeConfig(c *gin.Context) {
	var payload struct {
		Host                string                 `json:"host"`
		Port                int                    `json:"port"`
		LogLevel            string                 `json:"logLevel"`
		HTTPTimeout         *int                   `json:"httpTimeout"`
		PanelAccessToken    *string                `json:"panelAccessToken"`
		DatabasePath        *string                `json:"databasePath"`
		EnablePprof         *bool                  `json:"enablePprof"`
		AllowFakeIPOutbound *bool                  `json:"allowFakeIPOutbound"`
		UsageLog            *config.UsageLogConfig `json:"usageLog"`
		ModelCatalog        *struct {
			SyncIntervalMinutes *int `json:"syncIntervalMinutes"`
		} `json:"modelCatalog"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	server := s.config.GetServer()
	requestsRestart := (payload.Host != "" && payload.Host != server.Host) || (payload.Port != 0 && payload.Port != server.Port)
	// 两阶段：先整体校验，全部通过后再应用——避免「后段字段非法/保存失败
	// 但前段已生效」造成的内存与磁盘状态分叉（面板令牌等即时字段尤其敏感）。
	if payload.HTTPTimeout != nil && *payload.HTTPTimeout < 0 {
		respondFail(c, 400, "invalid_http_timeout", "httpTimeout must not be negative")
		return
	}
	if payload.Port != 0 && (payload.Port < 1 || payload.Port > 65535) {
		respondFail(c, 400, "invalid_port", "port must be between 1 and 65535")
		return
	}
	if payload.LogLevel != "" {
		switch payload.LogLevel {
		case "debug", "info", "warn", "error":
		default:
			// logAt 对未知级别静默按 info 兜底，保存成功但设置无效——
			// 这里直接拒绝，避免「改了没生效」的困惑。
			respondFail(c, 400, "invalid_log_level", "logLevel must be one of debug/info/warn/error")
			return
		}
	}
	if payload.DatabasePath != nil && strings.TrimSpace(*payload.DatabasePath) == "" {
		respondFail(c, 400, "invalid_database_path", "databasePath must not be empty; unset it in config.json to use the default")
		return
	}
	if payload.Host != "" && strings.TrimSpace(payload.Host) == "" {
		respondFail(c, 400, "invalid_host", "host must not be blank")
		return
	}
	if payload.PanelAccessToken != nil && strings.TrimSpace(*payload.PanelAccessToken) == "" {
		// 空面板令牌会让 IsValidPanelAccessToken 对一切请求返回 false，
		// 直接锁死整个管理面板，只能去服务器手改 config.json 恢复。
		respondFail(c, 400, "invalid_panel_access_token", "panel access token must not be empty")
		return
	}
	if payload.ModelCatalog != nil && payload.ModelCatalog.SyncIntervalMinutes != nil && *payload.ModelCatalog.SyncIntervalMinutes < 0 {
		respondFail(c, 400, "invalid_sync_interval", "syncIntervalMinutes must not be negative")
		return
	}
	// usageLog：数值字段全部非负（0 是合法的「关闭/不保存」语义）。
	if payload.UsageLog != nil {
		for _, check := range []struct {
			name  string
			value *int
		}{
			{"retentionDays", payload.UsageLog.RetentionDays},
			{"maxStorageMB", payload.UsageLog.MaxStorageMB},
			{"maxRecords", payload.UsageLog.MaxRecords},
			{"bodyMaxKB", payload.UsageLog.BodyMaxKB},
			{"cleanupIntervalMinutes", payload.UsageLog.CleanupIntervalMinutes},
		} {
			if check.value != nil && *check.value < 0 {
				respondFail(c, 400, "invalid_usage_log", check.name+" must not be negative")
				return
			}
		}
	}

	if payload.LogLevel != "" {
		s.config.SetLogLevel(payload.LogLevel)
	}
	if payload.HTTPTimeout != nil {
		seconds := *payload.HTTPTimeout
		s.config.SetHTTPTimeout(seconds)
		// 即时下发到三个 relay adapter，运行时修改无需重启。
		timeout := time.Duration(seconds) * time.Second
		s.openaiAdapter.SetTimeout(timeout)
		s.claudeAdapter.SetTimeout(timeout)
		s.geminiAdapter.SetTimeout(timeout)
	}
	if payload.PanelAccessToken != nil {
		s.config.SetPanelAccessToken(*payload.PanelAccessToken)
	}
	if payload.DatabasePath != nil {
		old := s.config.GetDatabasePath()
		s.config.SetDatabasePath(*payload.DatabasePath)
		if s.config.GetDatabasePath() != old {
			requestsRestart = true
		}
	}
	if payload.EnablePprof != nil {
		s.config.SetEnablePprof(*payload.EnablePprof)
		// pprof 路由在进程启动时挂载，运行时修改只有重启后生效。
		requestsRestart = true
	}
	if payload.AllowFakeIPOutbound != nil {
		s.config.SetAllowFakeIPOutbound(*payload.AllowFakeIPOutbound)
		// 即时下发到 relay 包级开关，无需重启。
		s.syncRelaySSRFPolicy()
	}
	if payload.UsageLog != nil {
		// 局部更新：仅覆盖显式提供的字段。BodyMaxKB/开关对后续请求即时生效；
		// 清理参数由后台巡检在下一 tick 重新读取。无需重启。
		s.config.SetUsageLogConfig(*payload.UsageLog)
	}
	if payload.ModelCatalog != nil && payload.ModelCatalog.SyncIntervalMinutes != nil {
		// 周期检查是动态的，写入配置即生效（0 = 默认 24h），无需重启。
		s.config.SetModelCatalogSyncInterval(*payload.ModelCatalog.SyncIntervalMinutes)
	}
	// host/port：此前只用于计算 restartRequired 而从未应用——设置页保存后
	// 表单闪回旧值、重启后监听也没变。现在即时应用到配置并随 Save() 落盘；
	// 监听套接字仍需重启切换（restartRequired 语义不变）。
	if payload.Host != "" && payload.Host != server.Host {
		s.config.SetHost(payload.Host)
	}
	if payload.Port != 0 && payload.Port != server.Port {
		s.config.SetPort(payload.Port)
	}
	if requestsRestart {
		s.markRestartRequired()
	}
	if err := s.config.Save(); err != nil {
		respondFail(c, 500, "save_config_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"updated": true, "restartRequired": requestsRestart})
}

func (s *Server) adminReload(c *gin.Context) { s.reloadConfig(c) }

// markRestartRequired 记录"存在需要重启才能生效的变更"，
// 供 /api/admin/restart-required/check 查询。
func (s *Server) markRestartRequired() {
	s.restartMu.Lock()
	s.restartRequired = true
	s.restartMu.Unlock()
}

func (s *Server) adminRestartRequired(c *gin.Context) {
	s.restartMu.Lock()
	pending := s.restartRequired
	s.restartMu.Unlock()
	respondOK(c, gin.H{"restartRequired": pending})
}

func (s *Server) adminListSources(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListSources(c.Request.Context())
	if err != nil {
		respondFail(c, 500, "list_sources_failed", err.Error())
		return
	}
	// 叠加运行时拉取状态（后台任务进行中/最近结果），内嵌结构体 JSON 展平，
	// 前端在源对象上直接读 refreshState 轮询进度。
	type sourceWithRefresh struct {
		storage.ModelSource
		RefreshState sourceRefreshState `json:"refreshState"`
	}
	withState := make([]sourceWithRefresh, len(items))
	for i := range items {
		withState[i] = sourceWithRefresh{
			ModelSource:  items[i],
			RefreshState: s.sourceRefreshStateOf(items[i].ID),
		}
	}
	respondOK(c, gin.H{"items": withState})
}

func (s *Server) adminUpsertSource(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.ModelSource
	if err := c.ShouldBindJSON(&item); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if id := c.Param("id"); id != "" {
		item.ID = id
	}
	if item.ID == "" {
		item.ID = slugID(item.Name)
	}
	if err := validateCustomSourceProtocol(&item); err != nil {
		respondFail(c, 400, "invalid_custom_protocol_source", err.Error())
		return
	}
	// 「留空即不变」：编辑时若未填 apiKey，保留已有记录的原 key，避免被清空。
	if strings.TrimSpace(item.APIKey) == "" {
		existing, found, findErr := s.findSourceByID(c.Request.Context(), item.ID)
		if findErr != nil {
			// 查询失败时绝不能带着空 key 继续保存（会把存量密钥抹掉）。
			respondFail(c, 500, "lookup_source_failed", findErr.Error())
			return
		}
		if found {
			item.APIKey = existing.APIKey
		}
	}
	if err := store.UpsertSource(c.Request.Context(), item); err != nil {
		respondFail(c, 400, "save_source_failed", err.Error())
		return
	}
	s.invalidateRouteCache()

	// 保存后自动同步该源的模型到模型缓存，省去用户额外手动刷新：
	//   - 手动源：同步写入（不涉网络），保证保存响应返回后前端立即能读到新模型；
	//   - 自动拉取源：走后台任务管理器（refresh_jobs.go）异步拉取——与手动拉取
	//     共享去重/并发约束，结果落在源的 refreshState 上（前端可见进度与成败）。
	saved := item
	if !saved.AutoFetchModels {
		if _, err := s.refreshSourceByValue(c.Request.Context(), saved); err != nil {
			if s.store != nil {
				_ = s.store.InsertSystemLog(c.Request.Context(), "warn", "manual model sync after save failed", map[string]any{"sourceId": saved.ID, "sourceName": saved.Name, "error": err.Error()})
			}
		} else {
			s.invalidateRouteCache()
		}
	} else {
		s.launchSourceRefresh(saved)
	}

	respondOK(c, item)
}

func validateCustomSourceProtocol(item *storage.ModelSource) error {
	if item == nil {
		return fmt.Errorf("model source is nil")
	}
	platform := relay.NormalizeAPIFormat(item.Platform)
	if !strings.HasPrefix(platform, "custom:") {
		return nil
	}
	protocolID := strings.TrimPrefix(platform, "custom:")
	if _, ok := relay.GetCustomProtocol(protocolID); !ok {
		return fmt.Errorf("custom protocol %q is not registered in config.json", protocolID)
	}
	if item.AutoFetchModels {
		return fmt.Errorf("custom protocol sources require autoFetchModels=false and manualModels")
	}
	item.Platform = platform
	return nil
}

// findSourceByID 按 id 查找模型源（用于「留空即不变」保留原 secret）。
// 查询失败必须显式报错：把错误当「不存在」会让调用方把留空的 api_key 当
// 「不需要保留」直接写空，静默抹掉存量密钥。
func (s *Server) findSourceByID(ctx context.Context, id string) (storage.ModelSource, bool, error) {
	if s.store == nil || id == "" {
		return storage.ModelSource{}, false, nil
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return storage.ModelSource{}, false, err
	}
	for _, src := range sources {
		if src.ID == id {
			return src, true, nil
		}
	}
	return storage.ModelSource{}, false, nil
}

func (s *Server) adminDeleteSource(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteSource(c.Request.Context(), c.Param("id")); err != nil {
		respondFail(c, 500, "delete_source_failed", err.Error())
		return
	}
	// 源级多 key RR 游标一并清理：游标以 sourceID 为键，删除/重建循环下
	// 不清理会让 map 随历史源数量无限增长。
	s.keyRRMutex.Lock()
	delete(s.keyRRIndex, c.Param("id"))
	s.keyRRMutex.Unlock()
	s.invalidateRouteCache()
	respondOK(c, gin.H{"deleted": true})
}

// adminFetchSource 发起指定源的后台模型拉取：**立即返回**，任务在后台执行
// （refresh_jobs.go），前端轮询源列表的 refreshState 获取进度与结果。慢上游
// 不再阻塞界面；同一源重复触发会被去重。
func (s *Server) adminFetchSource(c *gin.Context) {
	if _, okStore := s.requireStore(c); !okStore {
		return
	}
	started, alreadyRunning, err := s.startSourceRefreshByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		respondFail(c, status, "fetch_source_failed", err.Error())
		return
	}
	if alreadyRunning {
		respondOK(c, gin.H{"started": false, "alreadyRunning": true})
		return
	}
	respondOK(c, gin.H{"started": started})
}

// adminRefreshModels 为所有启用的源发起后台拉取，立即返回启动数量。
// 各源独立成任务（源间并发受全局信号量约束），单个源失败只记日志与状态，
// 不影响其他源——返回值不再携带失败明细，结果见各源 refreshState 与系统日志。
func (s *Server) adminRefreshModels(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	sources, err := store.ListSources(c.Request.Context())
	if err != nil {
		respondFail(c, 500, "refresh_models_failed", err.Error())
		return
	}
	enabled := 0
	started := 0
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		enabled++
		if s.launchSourceRefresh(source) {
			started++
		}
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"started": started, "total": enabled})
}

// adminSetSourceEnabled 仅切换源启停：专用轻量端点，避免整源 PUT 附带的
// 「保存后自动同步模型」副作用（启停与模型列表无关，重拉上游可能触发限流）。
func (s *Server) adminSetSourceEnabled(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if payload.Enabled == nil {
		respondFail(c, 400, "enabled_required", "enabled field is required")
		return
	}
	found, err := store.UpdateSourceEnabled(c.Request.Context(), c.Param("id"), *payload.Enabled)
	if err != nil {
		respondFail(c, 500, "set_source_enabled_failed", err.Error())
		return
	}
	if !found {
		respondFail(c, 404, "source_not_found", "model source not found")
		return
	}
	// 路由装配按源 enabled 过滤模型（ListModels 的 join 条件），必须失效。
	s.invalidateRouteCache()
	respondOK(c, gin.H{"updated": true, "enabled": *payload.Enabled})
}

// adminModelCatalogStatus 返回能力目录（models.dev）的运行状态（方向1）。
func (s *Server) adminModelCatalogStatus(c *gin.Context) {
	respondOK(c, s.catalog.Status())
}

// adminModelCatalogRefresh 立即更新能力目录（绕过周期/退避），返回最新状态。
// 已有拉取进行中时等待其完成（有限时），不会重复发起。
func (s *Server) adminModelCatalogRefresh(c *gin.Context) {
	status, refreshed := s.catalog.Refresh()
	if !refreshed {
		if disabled, _ := status["enabled"].(bool); !disabled {
			respondFail(c, 400, "catalog_disabled", "model catalog is disabled")
			return
		}
	}
	respondOK(c, gin.H{"refreshed": refreshed, "status": status})
}

func (s *Server) adminListModels(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	filter := storage.ModelListFilter{
		SourceID: strings.TrimSpace(c.Query("sourceId")),
		Search:   strings.TrimSpace(c.Query("search")),
	}
	items, err := store.ListModelsFiltered(c.Request.Context(), filter)
	if err != nil {
		respondFail(c, 500, "list_models_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"items": items})
}

// adminUpdateModel 单模型部分更新（方向4）：名称/类型/能力/maxTokens/启停。
// 能力字段被修改时 capability_source 置 manual，后续刷新保留用户值。
func (s *Server) adminUpdateModel(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var payload struct {
		Name             *string `json:"name"`
		Type             *string `json:"type"`
		MaxTokens        *int    `json:"maxTokens"`
		VisionCapable    *bool   `json:"visionCapable"`
		ToolsCapable     *bool   `json:"toolsCapable"`
		StructuredOutput *bool   `json:"structuredOutput"`
		ThinkingMode     *string `json:"thinkingMode"`
		Enabled          *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	patch := storage.ModelPatch{
		Name:             payload.Name,
		Type:             payload.Type,
		MaxTokens:        payload.MaxTokens,
		VisionCapable:    payload.VisionCapable,
		ToolsCapable:     payload.ToolsCapable,
		StructuredOutput: payload.StructuredOutput,
		ThinkingMode:     payload.ThinkingMode,
		Enabled:          payload.Enabled,
	}
	if patch.ThinkingMode != nil && *patch.ThinkingMode == "" {
		respondFail(c, 400, "invalid_thinking_mode", "thinkingMode must not be empty")
		return
	}
	modelID := c.Query("modelId")
	if modelID == "" {
		respondFail(c, 400, "model_id_required", "modelId query parameter is required")
		return
	}
	found, err := store.UpdateModel(c.Request.Context(), modelID, c.Param("sourceId"), patch)
	if err != nil {
		respondFail(c, 500, "update_model_failed", err.Error())
		return
	}
	if !found {
		respondFail(c, 404, "model_not_found", "model not found in source")
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"updated": true})
}

// adminDeleteModel 删除单个模型（事务内同步清理组内引用，方向4）。
func (s *Server) adminDeleteModel(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	modelID := c.Query("modelId")
	if modelID == "" {
		respondFail(c, 400, "model_id_required", "modelId query parameter is required")
		return
	}
	deleted, err := store.DeleteModel(c.Request.Context(), modelID, c.Param("sourceId"))
	if err != nil {
		respondFail(c, 500, "delete_model_failed", err.Error())
		return
	}
	if !deleted {
		respondFail(c, 404, "model_not_found", "model not found in source")
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"deleted": true})
}

// adminAddGroupMembers 向现有组追加成员（方向3：批量「添加到已有组」原子端点）。
func (s *Server) adminAddGroupMembers(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var payload struct {
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if len(payload.Models) == 0 {
		respondFail(c, 400, "models_required", "models list must not be empty")
		return
	}
	added, err := store.AddGroupMembers(c.Request.Context(), c.Param("id"), payload.Models)
	if err != nil {
		respondFail(c, 400, "add_group_models_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"added": added})
}

// adminRemoveGroupMembers 从组移除成员（方向3）。
func (s *Server) adminRemoveGroupMembers(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var payload struct {
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if len(payload.Models) == 0 {
		respondFail(c, 400, "models_required", "models list must not be empty")
		return
	}
	removed, err := store.RemoveGroupMembers(c.Request.Context(), c.Param("id"), payload.Models)
	if err != nil {
		respondFail(c, 400, "remove_group_models_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"removed": removed})
}

func (s *Server) adminListGroups(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListGroups(c.Request.Context())
	if err != nil {
		respondFail(c, 500, "list_groups_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"items": items})
}

func (s *Server) adminUpsertGroup(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.ModelGroup
	if err := c.ShouldBindJSON(&item); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if id := c.Param("id"); id != "" {
		item.ID = id
	}
	if item.ID == "" {
		item.ID = slugID(item.Name)
	}
	if err := store.UpsertGroup(c.Request.Context(), item); err != nil {
		respondFail(c, 400, "save_group_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	respondOK(c, item)
}

func (s *Server) adminDeleteGroup(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteGroup(c.Request.Context(), c.Param("id")); err != nil {
		respondFail(c, 500, "delete_group_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	s.forgetGroupRuntimeState(c.Param("id"))
	respondOK(c, gin.H{"deleted": true})
}

func (s *Server) adminListTokens(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListAPITokens(c.Request.Context())
	if err != nil {
		respondFail(c, 500, "list_tokens_failed", err.Error())
		return
	}
	for i := range items {
		items[i].Token = maskSecret(items[i].Token)
	}
	respondOK(c, gin.H{"items": items})
}

// adminRevealToken 在 dashboard 鉴权下返回指定 API Key 的完整明文，
// 供前端"复制"按钮按需取用（列表默认仍脱敏，不在页面常驻明文）。
func (s *Server) adminRevealToken(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	item, found, err := store.FindAPITokenByName(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondFail(c, 500, "reveal_token_failed", err.Error())
		return
	}
	if !found {
		respondFail(c, 404, "token_not_found", "api key not found")
		return
	}
	respondOK(c, gin.H{"name": item.Name, "token": item.Token})
}

func (s *Server) adminUpsertToken(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.APIToken
	if err := c.ShouldBindJSON(&item); err != nil {
		respondFail(c, 400, "invalid_json", err.Error())
		return
	}
	if name := c.Param("name"); name != "" {
		item.Name = name
	}
	// 「留空即不变」：编辑时若未填 token，保留原值（不清空）。
	isNew := true
	if strings.TrimSpace(item.Token) == "" {
		if existing, found, err := store.FindAPITokenByName(c.Request.Context(), item.Name); err == nil && found {
			item.Token = existing.Token
			isNew = false
		}
	}
	// 新建时空 token 是死行（hash 为空、认证永远不命中）：直接拒绝而不是
	// 落一条不可用的记录。编辑路径的「留空即不变」不受影响。
	if isNew && strings.TrimSpace(item.Token) == "" {
		respondFail(c, 400, "invalid_token", "token must not be empty when creating a new API token")
		return
	}
	if err := store.UpsertAPIToken(c.Request.Context(), item); err != nil {
		respondFail(c, 400, "save_token_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	item.Token = maskSecret(item.Token)
	respondOK(c, item)
}

func (s *Server) adminDeleteToken(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteAPIToken(c.Request.Context(), c.Param("name")); err != nil {
		respondFail(c, 500, "delete_token_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"deleted": true})
}

// adminUsageTrend 按请求方提供的固定 UTC offset 聚合本地日趋势。
func (s *Server) adminUsageTrend(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	utcOffsetMinutes, okOffset := parseUTCOffsetMinutes(c)
	if !okOffset {
		return
	}
	buckets, err := store.UsageDaily(c.Request.Context(), usageQueryFromRequest(c), utcOffsetMinutes)
	if err != nil {
		respondFail(c, 500, "usage_trend_failed", err.Error())
		return
	}
	respondOK(c, buckets)
}

func parseUTCOffsetMinutes(c *gin.Context) (int, bool) {
	utcOffsetMinutes, err := strconv.Atoi(c.DefaultQuery("utcOffsetMinutes", "0"))
	if err != nil || utcOffsetMinutes < -14*60 || utcOffsetMinutes > 14*60 {
		respondFail(c, 400, "invalid_utc_offset", "utcOffsetMinutes must be an integer between -840 and 840")
		return 0, false
	}
	return utcOffsetMinutes, true
}

// adminUsagePulse 短窗按分钟桶聚合 RPM 与时延（平均 / P95）。
func (s *Server) adminUsagePulse(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	utcOffsetMinutes, okOffset := parseUTCOffsetMinutes(c)
	if !okOffset {
		return
	}
	bucketMinutes, err := strconv.Atoi(c.DefaultQuery("bucketMinutes", "1"))
	if err != nil || (bucketMinutes != 1 && bucketMinutes != 5 && bucketMinutes != 15) {
		respondFail(c, 400, "invalid_bucket", "bucketMinutes must be 1, 5, or 15")
		return
	}
	query := usageQueryFromRequest(c)
	if err := storage.ValidatePulseQuery(query); err != nil {
		code := "invalid_from"
		if errors.Is(err, storage.ErrPulseWindowTooLong) {
			code = "window_too_long"
		} else if errors.Is(err, storage.ErrPulseInvertedRange) {
			code = "invalid_range"
		}
		respondFail(c, 400, code, err.Error())
		return
	}
	result, err := store.UsagePulse(c.Request.Context(), query, utcOffsetMinutes, bucketMinutes)
	if err != nil {
		respondFail(c, 500, "usage_pulse_failed", err.Error())
		return
	}
	respondOK(c, result)
}

// adminUsageByModelDaily 按本地日 × 模型聚合；top 之外的模型合并为 isOther=true。
func (s *Server) adminUsageByModelDaily(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	utcOffsetMinutes, okOffset := parseUTCOffsetMinutes(c)
	if !okOffset {
		return
	}
	top := 8
	if raw := c.Query("top"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			respondFail(c, 400, "invalid_top", "top must be an integer between 1 and 50")
			return
		}
		top = n
	}
	buckets, err := store.UsageByModelDaily(c.Request.Context(), usageQueryFromRequest(c), utcOffsetMinutes, top)
	if err != nil {
		respondFail(c, 500, "usage_by_model_daily_failed", err.Error())
		return
	}
	respondOK(c, buckets)
}

// adminUsageByModel 按模型聚合（热门模型 / 明细表），支持与 stats 相同的时间与多选筛选。
func (s *Server) adminUsageByModel(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	buckets, err := store.UsageByModel(c.Request.Context(), usageQueryFromRequest(c))
	if err != nil {
		respondFail(c, 500, "usage_by_model_failed", err.Error())
		return
	}
	respondOK(c, buckets)
}

func (s *Server) adminUsageStats(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	summary, err := store.UsageTotals(c.Request.Context(), usageQueryFromRequest(c))
	if err != nil {
		respondFail(c, 500, "usage_stats_failed", err.Error())
		return
	}
	respondOK(c, summary)
}

func (s *Server) adminUsageLogs(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	total, items, err := store.QueryUsageLogs(c.Request.Context(), usageQueryFromRequest(c))
	if err != nil {
		respondFail(c, 500, "usage_logs_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"total": total, "items": items})
}

func (s *Server) adminUsageLogDetail(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	payload, found, err := store.GetUsageRecordJSON(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondFail(c, 500, "usage_detail_failed", err.Error())
		return
	}
	if !found {
		respondFail(c, 404, "usage_log_not_found", "usage log not found")
		return
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		respondFail(c, 500, "usage_detail_decode_failed", err.Error())
		return
	}
	respondOK(c, value)
}

func (s *Server) adminUsageSeq(c *gin.Context) {
	respondOK(c, gin.H{"seq": s.usageSeq.Load()})
}

func (s *Server) adminUsageReset(c *gin.Context) { s.resetUsage(c) }

// ---- 日志管理（usage-assets / 占用状态 / 手动清理）----

// adminUsageAsset 下发某条记录的外置媒体文件。资产是捕获的用户内容，
// 必须留在管理鉴权之后，绝不做公开静态目录。文件名格式严格校验
// （16 位 hex 哈希 + 白名单扩展名），requestId 拒绝路径分隔符，防穿越。
func (s *Server) adminUsageAsset(c *gin.Context) {
	requestID := c.Param("requestId")
	if requestID == "" || strings.ContainsAny(requestID, `/\.`) {
		respondFail(c, 400, "invalid_request_id", "invalid request id")
		return
	}
	fileName := c.Param("file")
	hash, ext, ok := parseAssetFileName(fileName)
	if !ok {
		respondFail(c, 400, "invalid_asset_name", "invalid asset file name")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.usageAssetsRoot(), requestID, hash+"."+ext))
	if err != nil {
		respondFail(c, 404, "asset_not_found", "asset file not found")
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Data(http.StatusOK, mimeByAssetExt(ext), data)
}

func mimeByAssetExt(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg":
		return "audio/ogg"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// usageAssetsUsage 是资产目录的体积统计（WalkDir 全量遍历，带短 TTL 缓存）。
type usageAssetsUsage struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
}

const assetsUsageCacheTTL = 30 * time.Second

func (s *Server) usageAssetsUsage() usageAssetsUsage {
	s.assetsUsageMu.Lock()
	if time.Since(s.assetsUsageAt) < assetsUsageCacheTTL {
		cached := s.assetsUsage
		s.assetsUsageMu.Unlock()
		return cached
	}
	s.assetsUsageMu.Unlock()

	usage := usageAssetsUsage{}
	_ = filepath.WalkDir(s.usageAssetsRoot(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 目录缺失/权限等：统计尽力而为
		}
		if d.IsDir() {
			usage.Dirs++
			return nil
		}
		if info, err := d.Info(); err == nil {
			usage.Bytes += info.Size()
			usage.Files++
		}
		return nil
	})

	s.assetsUsageMu.Lock()
	s.assetsUsage, s.assetsUsageAt = usage, time.Now()
	s.assetsUsageMu.Unlock()
	return usage
}

// adminUsageStorage 返回日志占用状态与最近一轮清理结果（设置页展示）。
func (s *Server) adminUsageStorage(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	ctx := c.Request.Context()
	dbStats, dbErr := store.UsageDBPageStats(ctx)
	recordCount, countErr := store.CountUsageRecords(ctx)
	if dbErr != nil || countErr != nil {
		respondFail(c, 500, "storage_stats_failed", firstNonEmptyStr(dbErr, countErr))
		return
	}
	cfg := s.config.GetUsageLogConfig()
	resp := gin.H{
		"db": gin.H{
			"totalBytes":   dbStats.TotalBytes(),
			"logicalBytes": dbStats.LogicalBytes(),
			"pageCount":    dbStats.PageCount,
			"pageSize":     dbStats.PageSize,
			"freePages":    dbStats.FreePages,
		},
		"recordCount": recordCount,
		"assets":      s.usageAssetsUsage(),
		"config": gin.H{
			"persistEnabled":         cfg.PersistEnabled,
			"retentionDays":          cfg.RetentionDays,
			"maxStorageMB":           cfg.MaxStorageBytes / 1024 / 1024,
			"maxRecords":             cfg.MaxRecords,
			"bodyMaxKB":              cfg.BodyMaxBytes / 1024,
			"bodyOnErrorOnly":        cfg.BodyOnErrorOnly,
			"externalizeMedia":       cfg.ExternalizeMedia,
			"cleanupIntervalMinutes": int(cfg.CleanupInterval.Minutes()),
		},
	}
	if r := s.usageRetention; r != nil {
		resp["lastCleanup"] = r.snapshotStats()
	}
	respondOK(c, resp)
}

// adminUsageCleanup 手动触发一轮清理巡检（异步执行，防重入）。
func (s *Server) adminUsageCleanup(c *gin.Context) {
	if s.usageRetention == nil {
		respondFail(c, http.StatusServiceUnavailable, "retention_unavailable", "usage retention is not running")
		return
	}
	accepted := s.usageRetention.triggerAsync()
	respondOK(c, gin.H{"accepted": accepted})
}

func firstNonEmptyStr(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return "unknown error"
}

func (s *Server) adminSystemLogs(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	total, items, err := store.QuerySystemLogs(c.Request.Context(), parsePositiveInt(c.Query("limit"), 100), parsePositiveInt(c.Query("offset"), 0), c.Query("level"))
	if err != nil {
		respondFail(c, 500, "logs_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"total": total, "items": items})
}

func (s *Server) adminHealth(c *gin.Context) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	respondOK(c, gin.H{"status": "ok", "database": s.store != nil, "memory": gin.H{"alloc": mem.Alloc, "sys": mem.Sys, "numGC": mem.NumGC}})
}

func usageQueryFromRequest(c *gin.Context) storage.UsageQuery {
	from, to := usageTimeRange(c)
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "success" && status != "failed" {
		status = ""
	}
	// 多选筛选：QueryArray 收集重复出现的同名参数（?keyName=a&keyName=b）；
	// 为空时下沉到单值字段，保持与旧调用方（含遗留 /__usage 面板）的兼容。
	return storage.UsageQuery{
		From:       from,
		To:         to,
		Limit:      parsePositiveInt(c.Query("limit"), 50),
		Offset:     parsePositiveInt(c.Query("offset"), 0),
		KeyName:    c.Query("keyName"),
		KeyHash:    c.Query("keyHash"),
		GroupName:  firstNonEmpty(c.Query("groupName"), c.Query("modelGroup")),
		ModelName:  c.Query("modelName"),
		Status:     status,
		StatusCode: parsePositiveInt(c.Query("statusCode"), 0),
		KeyNames:   c.QueryArray("keyName"),
		GroupNames: firstNonEmptyArray(c.QueryArray("groupName"), c.QueryArray("modelGroup")),
		ModelNames: c.QueryArray("modelName"),
		SourceID:   c.Query("sourceId"),
		SourceIDs:  c.QueryArray("sourceId"),
	}
}

// firstNonEmptyArray 返回第一个非空切片，用于 groupName/modelGroup 两个别名取其一。
func firstNonEmptyArray(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func slugID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fmt.Sprintf("item-%d", time.Now().UnixNano())
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() == 0 || !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	// 清洗后为空（例如纯中文/纯符号名称），回退到时间戳 id，
	// 避免 item.ID 为空导致存储层误报 "id is required"。
	if slug == "" {
		return fmt.Sprintf("item-%d", time.Now().UnixNano())
	}
	return slug
}

func maskSecret(value string) string {
	if len(value) <= 8 {
		if value == "" {
			return ""
		}
		return "***"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
