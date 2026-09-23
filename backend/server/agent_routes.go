package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// 协议 Agent 的 HTTP/SSE 薄层：参数绑定 → 引擎调用 → agent.Event 编码为
// SSE。轮次生命周期与 HTTP 请求解耦（引擎侧后台运行），这里只做转发与
// 用量观察（观察者不随连接断开而停止，用量统计不丢）。

const (
	// AgentUsageKeyName 是 Agent 模型调用在统计页的 key_name 标签。
	AgentUsageKeyName = "AI 协议助手"
	// agentRelayMode 是 Agent 用量记录的 relay_mode 标记。
	agentRelayMode    = "agent-assist"
	agentMaxBodyBytes = 40 << 20 // 消息端点请求体上限（含附件 data URL）
	// sseKeepaliveInterval 是 SSE 空闲时的注释帧间隔。工具执行最长可达
	// 两分钟且期间零事件，中间代理会把静默连接掐断。
	sseKeepaliveInterval = 15 * time.Second
)

func (s *Server) setupAgentRoutes(admin *gin.RouterGroup) {
	// 列表与远程 REST 面共用带过滤分页的内核；面板不带参数即全量。
	admin.GET("/agent/sessions", s.listAgentSessionsFiltered)
	admin.POST("/agent/sessions", s.adminCreateAgentSession)
	admin.GET("/agent/sessions/:id", s.adminGetAgentSession)
	admin.PATCH("/agent/sessions/:id", s.adminUpdateAgentSession)
	admin.DELETE("/agent/sessions/:id", s.adminDeleteAgentSession)
	admin.DELETE("/agent/sessions/:id/messages", s.adminClearAgentMessages)
	admin.POST("/agent/sessions/:id/messages", s.adminSendAgentMessage)
	admin.POST("/agent/sessions/:id/approve", s.adminApproveAgentAction)
	admin.POST("/agent/sessions/:id/stop", s.adminStopAgentTurn)
	admin.POST("/agent/sessions/:id/restore-draft", s.adminRestoreAgentDraft)
}

// protocolAgentEngine 惰性装配引擎（store 就绪后首次调用时构建）。
func (s *Server) protocolAgentEngine() *agent.Engine {
	s.agentEngineOnce.Do(func() {
		if s.store == nil {
			return
		}
		registry, err := agent.NewRegistry(append(append(append(newProtocolAgentTools(s), newAgentOpsTools(s)...), newAgentTokenTools(s)...), &updatePlanTool{}, &askUserTool{}, &updateTitleTool{})...)
		if err != nil {
			log.Printf("agent engine tools unavailable: %v", err)
			return
		}
		s.agentEngineInst = agent.NewEngine(
			newAgentStreamCaller(s),
			s.store,
			registry,
			newAgentUserContentRenderer(s),
			agentSystemPrompt,
			agent.Options{MaxModelCalls: 12, TurnTimeout: 10 * time.Minute, ParseAsk: parseAskQuestion},
		)
		// 启动对账：上一进程崩溃/被杀遗留的 running 会话复位为 idle，否则
		// UI 会永远挡在不存在的轮次上（waiting_approval 保留可恢复）。
		s.agentEngineInst.ReconcileInterruptedSessions(context.Background())
	})
	return s.agentEngineInst
}

func (s *Server) requireAgentEngine(c *gin.Context) (*agent.Engine, bool) {
	if _, ok := s.requireStore(c); !ok {
		return nil, false
	}
	engine := s.protocolAgentEngine()
	if engine == nil {
		respondFail(c, http.StatusServiceUnavailable, "agent_unavailable", "Agent 引擎不可用（存储未就绪）")
		return nil, false
	}
	return engine, true
}

// agentSessionView 是对外（列表/详情）的会话视图：凭证只给布尔标记。
func agentSessionView(session *agent.Session) gin.H {
	return gin.H{
		"id": session.ID, "title": session.Title, "mode": session.Mode, "protocolId": session.ProtocolID,
		"seedConfig":    json.RawMessage(session.SeedConfig),
		"draftConfig":   json.RawMessage(session.DraftConfig),
		"draftRestore":  json.RawMessage(session.DraftRestore),
		"settings":      session.Settings,
		"status":        session.Status,
		"pendingAction": agent.MaskedPendingAction(session.PendingAction),
		"plan":          session.Plan,
		"userTurns":     session.UserTurns,
		"totalTokens":   session.TotalTokens,
		"createdAt":     session.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     session.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type agentSessionCreatePayload struct {
	Title      string          `json:"title,omitempty"`
	Mode       string          `json:"mode,omitempty"`       // create（默认）| edit
	ProtocolID string          `json:"protocolId,omitempty"` // 编辑模式必填
	Settings   *agent.Settings `json:"settings,omitempty"`
}

func (s *Server) adminCreateAgentSession(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	var payload agentSessionCreatePayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		mode = agent.ModeCreate
	}
	seed := ""
	protocolID := strings.TrimSpace(payload.ProtocolID)
	if mode == agent.ModeEdit {
		if protocolID == "" {
			respondFail(c, http.StatusBadRequest, "missing_protocol", "编辑模式必须指定 protocolId")
			return
		}
		rows, err := store.ListCustomProtocols(c.Request.Context())
		if err != nil {
			respondFail(c, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		found := false
		for _, row := range rows {
			if strings.EqualFold(row.ID, protocolID) {
				seed, protocolID, found = row.Config, row.ID, true
				break
			}
		}
		if !found {
			respondFail(c, http.StatusNotFound, "not_found", fmt.Sprintf("协议 %q 不存在", protocolID))
			return
		}
	}
	upsert := storage.AgentSessionUpsert{
		Title:      strings.TrimSpace(payload.Title),
		Mode:       mode,
		ProtocolID: protocolID,
		SeedConfig: seed,
	}
	// 模型字段可不带：首条消息前 UI 会引导选择。
	if payload.Settings != nil {
		upsert.Settings = *payload.Settings
	}
	session, err := store.CreateAgentSession(c.Request.Context(), upsert)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	respondOK(c, agentSessionView(session))
}

func (s *Server) adminGetAgentSession(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	session, err := store.GetAgentSession(c.Request.Context(), id)
	if err != nil {
		respondFail(c, http.StatusNotFound, "not_found", fmt.Sprintf("会话 %q 不存在", id))
		return
	}
	messages, err := store.ListMessages(c.Request.Context(), id)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	view := agentSessionView(session)
	if engine := s.protocolAgentEngine(); engine != nil && engine.IsRunning(id) {
		view["status"] = agent.StatusRunning
	}
	respondOK(c, gin.H{"session": view, "messages": messages})
}

type agentSessionUpdatePayload struct {
	Title       *string              `json:"title,omitempty"`
	Settings    *agent.SettingsPatch `json:"settings,omitempty"`
	APIKey      *string              `json:"apiKey,omitempty"`
	ClearAPIKey bool                 `json:"clearApiKey,omitempty"`
}

func (s *Server) adminUpdateAgentSession(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	var payload agentSessionUpdatePayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if payload.Settings != nil && payload.Settings.ThinkingEffort != nil {
		effort := strings.ToLower(strings.TrimSpace(*payload.Settings.ThinkingEffort))
		switch effort {
		case "", "low", "medium", "high", "max", "adaptive":
			*payload.Settings.ThinkingEffort = effort
		default:
			respondFail(c, http.StatusBadRequest, "invalid_effort", "thinkingEffort 可选值：low/medium/high/max/adaptive")
			return
		}
	}
	session, err := store.UpdateAgentSessionSettings(c.Request.Context(), strings.TrimSpace(c.Param("id")),
		payload.Title, payload.Settings, payload.APIKey, payload.ClearAPIKey)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	respondOK(c, agentSessionView(session))
}

// adminRestoreAgentDraft 把草稿回滚到最近一轮修改前的还原点。
func (s *Server) adminRestoreAgentDraft(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if engine := s.protocolAgentEngine(); engine != nil && engine.IsRunning(id) {
		respondFail(c, http.StatusConflict, "session_running", "会话轮次进行中，无法还原草稿")
		return
	}
	session, err := store.GetAgentSession(c.Request.Context(), id)
	if err != nil {
		respondFail(c, http.StatusNotFound, "not_found", fmt.Sprintf("会话 %q 不存在", id))
		return
	}
	if len(session.DraftRestore) == 0 {
		respondFail(c, http.StatusConflict, "no_restore_point", "本会话还没有可用的草稿还原点")
		return
	}
	if err := store.UpdateSessionState(c.Request.Context(), id, agent.SessionStateUpdate{DraftConfig: session.DraftRestore}); err != nil {
		respondFail(c, http.StatusInternalServerError, "restore_failed", err.Error())
		return
	}
	updated, err := store.GetAgentSession(c.Request.Context(), id)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "restore_failed", err.Error())
		return
	}
	respondOK(c, agentSessionView(updated))
}

func (s *Server) adminDeleteAgentSession(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if engine := s.protocolAgentEngine(); engine != nil {
		engine.Stop(id) // 删除前停轮次
	}
	deleted, err := store.DeleteAgentSession(c.Request.Context(), id)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	if !deleted {
		respondFail(c, http.StatusNotFound, "not_found", fmt.Sprintf("会话 %q 不存在", id))
		return
	}
	respondOK(c, gin.H{"deleted": true})
}

// adminClearAgentMessages 清空（默认）或截断会话消息：afterSeq=0 即全清，
// 保留会话与草稿。
func (s *Server) adminClearAgentMessages(c *gin.Context) {
	store, ok := s.requireStore(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	afterSeq := 0
	if raw := strings.TrimSpace(c.Query("afterSeq")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			afterSeq = parsed
		}
	}
	if engine := s.protocolAgentEngine(); engine != nil && engine.IsRunning(id) {
		respondFail(c, http.StatusConflict, "session_running", "会话轮次进行中，请先停止")
		return
	}
	if err := store.TruncateMessages(c.Request.Context(), id, afterSeq); err != nil {
		respondFail(c, http.StatusInternalServerError, "truncate_failed", err.Error())
		return
	}
	respondOK(c, gin.H{"cleared": true})
}

type agentMessagePayload struct {
	Content   string           `json:"content,omitempty"`
	Documents []agent.Document `json:"documents,omitempty"`
	// AfterSeq 非 0 时先截断 seq > afterSeq 的消息再发送（编辑重发/重试/
	// 重新生成共用）。
	AfterSeq *int `json:"afterSeq,omitempty"`
}

// adminSendAgentMessage 发送用户消息并流式返回轮次事件（SSE）。
func (s *Server) adminSendAgentMessage(c *gin.Context) {
	engine, ok := s.requireAgentEngine(c)
	if !ok {
		return
	}
	var payload agentMessagePayload
	if err := bindAgentStreamJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if payload.AfterSeq != nil {
		if *payload.AfterSeq < 0 {
			respondFail(c, http.StatusBadRequest, "invalid_after_seq", "afterSeq 不能为负")
			return
		}
		if engine.IsRunning(id) {
			respondFail(c, http.StatusConflict, "session_running", "会话轮次进行中，无法截断重发")
			return
		}
		if err := s.store.TruncateMessages(c.Request.Context(), id, *payload.AfterSeq); err != nil {
			respondFail(c, http.StatusInternalServerError, "truncate_failed", err.Error())
			return
		}
	}
	if payload.Content == "" && len(payload.Documents) == 0 && payload.AfterSeq == nil {
		respondFail(c, http.StatusBadRequest, "empty_message", "消息内容不能为空")
		return
	}
	input := &agent.UserContent{Text: payload.Content, Documents: payload.Documents}
	events, err := engine.RunTurn(c.Request.Context(), id, input)
	if err != nil {
		respondAgentTurnError(c, err)
		return
	}
	s.streamAgentEvents(c, id, events)
}

type agentApprovePayload struct {
	Approved bool   `json:"approved"`
	BaseURL  string `json:"baseUrl,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	Note     string `json:"note,omitempty"`
	// Answer 是 ask_user 提问的用户作答（question 型暂停专用）。
	Answer string `json:"answer,omitempty"`
}

// adminApproveAgentAction 审批待定动作并流式返回续跑事件（SSE）。
func (s *Server) adminApproveAgentAction(c *gin.Context) {
	engine, ok := s.requireAgentEngine(c)
	if !ok {
		return
	}
	var payload agentApprovePayload
	if err := bindAgentStreamJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	events, err := engine.ResumeApproval(c.Request.Context(), id, agent.ApprovalDecision{
		Approved: payload.Approved, BaseURL: payload.BaseURL, APIKey: payload.APIKey, Note: payload.Note, Answer: payload.Answer,
	})
	if err != nil {
		respondAgentTurnError(c, err)
		return
	}
	s.streamAgentEvents(c, id, events)
}

func (s *Server) adminStopAgentTurn(c *gin.Context) {
	engine, ok := s.requireAgentEngine(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	stopped := engine.Stop(id)
	respondOK(c, gin.H{"stopped": stopped})
}

func respondAgentTurnError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agent.ErrSessionRunning):
		respondFail(c, http.StatusConflict, "session_running", "会话已有轮次进行中")
	case errors.Is(err, agent.ErrNoPendingApproval):
		respondFail(c, http.StatusConflict, "no_pending_approval", "会话没有等待审批的动作")
	case strings.Contains(err.Error(), "not found"):
		respondFail(c, http.StatusNotFound, "not_found", err.Error())
	default:
		// 存储故障等：500 而非把一切当作不存在。
		respondFail(c, http.StatusInternalServerError, "turn_failed", err.Error())
	}
}

// bindAgentStreamJSON 消息/审批端点的请求体绑定（上限比普通管理端点大，
// 容纳附件 data URL）。
func bindAgentStreamJSON(c *gin.Context, target any) error {
	defer c.Request.Body.Close()
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, agentMaxBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// streamAgentEvents 把引擎事件编码为 SSE 并转发。用量入账在模型调用层
// （agentStreamCaller.Call，成功与失败各记一条）；转发层不再重复观察。
func (s *Server) streamAgentEvents(c *gin.Context, sessionID string, events <-chan agent.Event) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	write := func(event agent.Event) bool {
		encoded, err := json.Marshal(event)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, encoded); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}
	// 连接断开即停止转发（轮次在引擎侧继续，刷新即可见结果）。空闲时
	// 发 SSE 注释帧保活：注释行不以 event/data 开头，前端解析器直接忽略。
	ctx := c.Request.Context()
	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			if !write(event) {
				return
			}
		}
	}
}
