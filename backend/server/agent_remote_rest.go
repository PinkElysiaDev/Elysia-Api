package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/elysia-api/backend/agent"

	"github.com/gin-gonic/gin"
)

// AI 助手远程面之一：REST（/api/agent/*）。与 /api/admin/agent/* 共用同一
// 组 handler（单一事实源），差别仅在鉴权链——这里要求 Bearer API key 且带
// agent 作用域（agentRemoteAuth），并受 config.agentRemote 总开关门控。
// 会话列表在这里补齐分页与状态过滤；管理面板不带查询参数，行为不变。

// setupAgentRemoteRoutes 注册 /api/agent 组（挂在 agentRemoteGate +
// agentRemoteAuth 之后）。
func (s *Server) setupAgentRemoteRoutes(group *gin.RouterGroup) {
	group.GET("/agent/sessions", s.listAgentSessionsFiltered)
	group.POST("/agent/sessions", s.adminCreateAgentSession)
	group.GET("/agent/sessions/:id", s.adminGetAgentSession)
	group.PATCH("/agent/sessions/:id", s.adminUpdateAgentSession)
	group.DELETE("/agent/sessions/:id", s.adminDeleteAgentSession)
	group.POST("/agent/sessions/:id/restore-draft", s.adminRestoreAgentDraft)
	group.DELETE("/agent/sessions/:id/messages", s.adminClearAgentMessages)
	group.POST("/agent/sessions/:id/messages", s.adminSendAgentMessage)
	group.POST("/agent/sessions/:id/approve", s.adminApproveAgentAction)
	group.POST("/agent/sessions/:id/stop", s.adminStopAgentTurn)
}

// listAgentSessionsFiltered 会话列表（管理面板与 /api/agent 共用）：
// 可选 status 过滤（引擎运行态口径，先叠加后过滤）与 limit/offset 分页；
// 不带参数返回全量。total 恒为过滤后总数（分页前）。
func (s *Server) listAgentSessionsFiltered(c *gin.Context) {
	filter := remoteAgentListFilter{Status: strings.TrimSpace(c.Query("status"))}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			respondFail(c, http.StatusBadRequest, "invalid_limit", "limit 必须是正整数")
			return
		}
		filter.Limit = limit
	}
	if filter.Status != "" && filter.Status != agent.StatusIdle && filter.Status != agent.StatusRunning && filter.Status != agent.StatusWaitingApproval {
		respondFail(c, http.StatusBadRequest, "invalid_status", "status 可选值：idle/running/waiting_approval")
		return
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			respondFail(c, http.StatusBadRequest, "invalid_offset", "offset 必须是非负整数")
			return
		}
		filter.Offset = offset
	}
	sessions, total, err := s.listRemoteAgentSessions(c.Request.Context(), filter)
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	items := make([]gin.H, 0, len(sessions))
	for i := range sessions {
		items = append(items, agentSessionView(&sessions[i]))
	}
	respondOK(c, gin.H{"items": items, "total": total})
}
