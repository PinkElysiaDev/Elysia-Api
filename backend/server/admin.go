package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type adminError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ok(c *gin.Context, data any) { c.JSON(http.StatusOK, gin.H{"ok": true, "data": data}) }

func fail(c *gin.Context, status int, code, message string) {
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
	admin.GET("/models", s.adminListModels)
	admin.POST("/models/refresh", s.adminRefreshModels)
	admin.GET("/model-groups", s.adminListGroups)
	admin.POST("/model-groups", s.adminUpsertGroup)
	admin.PUT("/model-groups/:id", s.adminUpsertGroup)
	admin.DELETE("/model-groups/:id", s.adminDeleteGroup)
	admin.GET("/api-tokens", s.adminListTokens)
	admin.GET("/api-tokens/:name/reveal", s.adminRevealToken)
	admin.POST("/api-tokens", s.adminUpsertToken)
	admin.PUT("/api-tokens/:name", s.adminUpsertToken)
	admin.DELETE("/api-tokens/:name", s.adminDeleteToken)
	admin.GET("/usage/stats", s.adminUsageStats)
	admin.GET("/usage/logs", s.adminUsageLogs)
	admin.GET("/usage/logs/:id", s.adminUsageLogDetail)
	admin.POST("/usage/reset", s.adminUsageReset)
	admin.GET("/logs", s.adminSystemLogs)
	admin.GET("/health", s.adminHealth)
}

func (s *Server) requireStore(c *gin.Context) (*storage.Store, bool) {
	if s.store == nil {
		fail(c, http.StatusServiceUnavailable, "store_unavailable", "sqlite store is unavailable")
		return nil, false
	}
	return s.store, true
}

func (s *Server) adminRuntimeConfig(c *gin.Context) {
	server := s.config.GetServer()
	ok(c, gin.H{
		"host":                server.Host,
		"port":                server.Port,
		"panelAccessToken":    s.config.GetPanelAccessToken(),
		"databasePath":        s.config.GetDatabasePath(),
		"defaultDatabasePath": s.config.GetDefaultDatabasePath(),
		"logLevel":            s.config.GetLogLevel(),
		"httpTimeout":         s.config.GetHTTPTimeout(),
		"enablePprof":         s.config.GetEnablePprof(),
		"allowFakeIPOutbound": s.config.IsFakeIPOutboundAllowed(),
	})
}

func (s *Server) adminUpdateRuntimeConfig(c *gin.Context) {
	var payload struct {
		Host                string  `json:"host"`
		Port                int     `json:"port"`
		LogLevel            string  `json:"logLevel"`
		HTTPTimeout         int     `json:"httpTimeout"`
		PanelAccessToken    *string `json:"panelAccessToken"`
		DatabasePath        *string `json:"databasePath"`
		EnablePprof         *bool   `json:"enablePprof"`
		AllowFakeIPOutbound *bool   `json:"allowFakeIPOutbound"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, 400, "invalid_json", err.Error())
		return
	}
	server := s.config.GetServer()
	restartRequired := (payload.Host != "" && payload.Host != server.Host) || (payload.Port != 0 && payload.Port != server.Port)
	if payload.LogLevel != "" {
		s.config.SetLogLevel(payload.LogLevel)
	}
	if payload.HTTPTimeout >= 0 {
		s.config.SetHTTPTimeout(payload.HTTPTimeout)
	}
	if payload.PanelAccessToken != nil {
		s.config.SetPanelAccessToken(*payload.PanelAccessToken)
	}
	if payload.DatabasePath != nil {
		old := s.config.GetDatabasePath()
		s.config.SetDatabasePath(*payload.DatabasePath)
		if s.config.GetDatabasePath() != old {
			restartRequired = true
		}
	}
	if payload.EnablePprof != nil {
		s.config.SetEnablePprof(*payload.EnablePprof)
		restartRequired = true
	}
	if payload.AllowFakeIPOutbound != nil {
		s.config.SetAllowFakeIPOutbound(*payload.AllowFakeIPOutbound)
		// 即时下发到 relay 包级开关，无需重启。
		s.syncRelaySSRFPolicy()
	}
	if err := s.config.Save(); err != nil {
		fail(c, 500, "save_config_failed", err.Error())
		return
	}
	ok(c, gin.H{"updated": true, "restartRequired": restartRequired})
}

func (s *Server) adminReload(c *gin.Context) { s.reloadConfig(c) }

func (s *Server) adminRestartRequired(c *gin.Context) { ok(c, gin.H{"restartRequired": false}) }

func (s *Server) adminListSources(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListSources(c.Request.Context())
	if err != nil {
		fail(c, 500, "list_sources_failed", err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

func (s *Server) adminUpsertSource(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.ModelSource
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, 400, "invalid_json", err.Error())
		return
	}
	if id := c.Param("id"); id != "" {
		item.ID = id
	}
	if item.ID == "" {
		item.ID = slugID(item.Name)
	}
	if err := validateCustomSourceProtocol(&item); err != nil {
		fail(c, 400, "invalid_custom_protocol_source", err.Error())
		return
	}
	// 「留空即不变」：编辑时若未填 apiKey，保留已有记录的原 key，避免被清空。
	if strings.TrimSpace(item.APIKey) == "" {
		if existing, found := s.findSourceByID(c.Request.Context(), item.ID); found {
			item.APIKey = existing.APIKey
		}
	}
	if err := store.UpsertSource(c.Request.Context(), item); err != nil {
		fail(c, 400, "save_source_failed", err.Error())
		return
	}
	s.invalidateRouteCache()

	// 保存后自动拉取一次该源的模型，省去用户额外手动刷新：
	//   - 自动拉取源：异步拉取（不阻塞保存响应），失败仅记日志；
	//   - 手动源：ReplaceSourceModels 同步 manualModels 到模型缓存。
	saved := item
	go func() {
		if _, err := s.refreshSourceByValue(context.Background(), saved); err != nil {
			if s.store != nil {
				_ = s.store.InsertSystemLog(context.Background(), "warn", "auto refresh after save failed", map[string]any{"sourceId": saved.ID, "sourceName": saved.Name, "error": err.Error()})
			}
			return
		}
		s.invalidateRouteCache()
	}()

	ok(c, item)
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
func (s *Server) findSourceByID(ctx context.Context, id string) (storage.ModelSource, bool) {
	if s.store == nil || id == "" {
		return storage.ModelSource{}, false
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return storage.ModelSource{}, false
	}
	for _, src := range sources {
		if src.ID == id {
			return src, true
		}
	}
	return storage.ModelSource{}, false
}

func (s *Server) adminDeleteSource(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteSource(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, 500, "delete_source_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	ok(c, gin.H{"deleted": true})
}

func (s *Server) adminFetchSource(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	count, err := s.refreshSource(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 500, "fetch_source_failed", err.Error())
		return
	}
	_ = store.InsertSystemLog(c.Request.Context(), "info", "model source refreshed", gin.H{"sourceId": c.Param("id"), "count": count})
	s.invalidateRouteCache()
	ok(c, gin.H{"refreshed": true, "count": count})
}

func (s *Server) adminListModels(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListModels(c.Request.Context())
	if err != nil {
		fail(c, 500, "list_models_failed", err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

func (s *Server) adminRefreshModels(c *gin.Context) {
	count, failures, err := s.refreshAllSources(c.Request.Context())
	if err != nil {
		fail(c, 500, "refresh_models_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	ok(c, gin.H{"refreshed": true, "count": count, "failures": failures})
}

func (s *Server) adminListGroups(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListGroups(c.Request.Context())
	if err != nil {
		fail(c, 500, "list_groups_failed", err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}

func (s *Server) adminUpsertGroup(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.ModelGroup
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, 400, "invalid_json", err.Error())
		return
	}
	if id := c.Param("id"); id != "" {
		item.ID = id
	}
	if item.ID == "" {
		item.ID = slugID(item.Name)
	}
	if err := store.UpsertGroup(c.Request.Context(), item); err != nil {
		fail(c, 400, "save_group_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	ok(c, item)
}

func (s *Server) adminDeleteGroup(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteGroup(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, 500, "delete_group_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	ok(c, gin.H{"deleted": true})
}

func (s *Server) adminListTokens(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	items, err := store.ListAPITokens(c.Request.Context())
	if err != nil {
		fail(c, 500, "list_tokens_failed", err.Error())
		return
	}
	for i := range items {
		items[i].Token = maskSecret(items[i].Token)
	}
	ok(c, gin.H{"items": items})
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
		fail(c, 500, "reveal_token_failed", err.Error())
		return
	}
	if !found {
		fail(c, 404, "token_not_found", "api key not found")
		return
	}
	ok(c, gin.H{"name": item.Name, "token": item.Token})
}

func (s *Server) adminUpsertToken(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	var item storage.APIToken
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, 400, "invalid_json", err.Error())
		return
	}
	if name := c.Param("name"); name != "" {
		item.Name = name
	}
	if name := c.Param("name"); name != "" {
		item.Name = name
	}
	// 「留空即不变」：编辑时若未填 token，保留原值（不清空）。
	if strings.TrimSpace(item.Token) == "" {
		if existing, found, err := store.FindAPITokenByName(c.Request.Context(), item.Name); err == nil && found {
			item.Token = existing.Token
		}
	}
	if err := store.UpsertAPIToken(c.Request.Context(), item); err != nil {
		fail(c, 400, "save_token_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	item.Token = maskSecret(item.Token)
	ok(c, item)
}

func (s *Server) adminDeleteToken(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if err := store.DeleteAPIToken(c.Request.Context(), c.Param("name")); err != nil {
		fail(c, 500, "delete_token_failed", err.Error())
		return
	}
	s.invalidateRouteCache()
	ok(c, gin.H{"deleted": true})
}

func (s *Server) adminUsageStats(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	summary, err := store.UsageTotals(c.Request.Context(), usageQueryFromRequest(c))
	if err != nil {
		fail(c, 500, "usage_stats_failed", err.Error())
		return
	}
	ok(c, summary)
}

func (s *Server) adminUsageLogs(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	total, items, err := store.QueryUsageLogs(c.Request.Context(), usageQueryFromRequest(c))
	if err != nil {
		fail(c, 500, "usage_logs_failed", err.Error())
		return
	}
	ok(c, gin.H{"total": total, "items": items})
}

func (s *Server) adminUsageLogDetail(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	payload, found, err := store.GetUsageRecordJSON(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 500, "usage_detail_failed", err.Error())
		return
	}
	if !found {
		fail(c, 404, "usage_log_not_found", "usage log not found")
		return
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		fail(c, 500, "usage_detail_decode_failed", err.Error())
		return
	}
	ok(c, value)
}

func (s *Server) adminUsageReset(c *gin.Context) { s.resetUsage(c) }

func (s *Server) adminSystemLogs(c *gin.Context) {
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	total, items, err := store.QuerySystemLogs(c.Request.Context(), parsePositiveInt(c.Query("limit"), 100), parsePositiveInt(c.Query("offset"), 0), c.Query("level"))
	if err != nil {
		fail(c, 500, "logs_failed", err.Error())
		return
	}
	ok(c, gin.H{"total": total, "items": items})
}

func (s *Server) adminHealth(c *gin.Context) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	ok(c, gin.H{"status": "ok", "database": s.store != nil, "memory": gin.H{"alloc": mem.Alloc, "sys": mem.Sys, "numGC": mem.NumGC}})
}

func usageQueryFromRequest(c *gin.Context) storage.UsageQuery {
	from, to := usageTimeRange(c)
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
		StatusCode: parsePositiveInt(c.Query("statusCode"), 0),
		KeyNames:   c.QueryArray("keyName"),
		GroupNames: firstNonEmptyArray(c.QueryArray("groupName"), c.QueryArray("modelGroup")),
		ModelNames: c.QueryArray("modelName"),
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
