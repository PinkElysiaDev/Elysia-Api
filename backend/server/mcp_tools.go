package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
)

// MCP 工具面：把远程服务层（remote_agent_service.go）的会话/轮次原语映射
// 为 MCP tools。工具名加 agent_ 前缀做命名空间；schema 沿用项目里
// objectSchema 的构造习惯。短工具同步返回 JSON；agent_send_message /
// agent_respond 驱动真实轮次，走 SSE 响应（progress 通知 + 终帧响应）。

// mcpTool 是一个 MCP 工具的静态定义与执行入口。
type mcpTool struct {
	name        string
	title       string
	description string
	schema      map[string]any
	// streaming=true 时 invoke 拿到 progress 回调并由传输层走 SSE。
	streaming bool
	// invoke 返回 structuredContent（map）；业务失败返回 error → isError。
	invoke func(ctx context.Context, s *Server, args json.RawMessage, progress func(message string)) (any, error)
}

// mcpToolset 惰性构建一次（定义静态）。
func mcpToolset() []mcpTool {
	return []mcpTool{
		{
			name:        "agent_list_sessions",
			title:       "列出 AI 助手会话",
			description: "列出 AI 助手（网关智能体）会话。可按状态过滤（idle/running/waiting_approval）与分页；返回会话视图（含 pendingAction 概要，凭证脱敏）。",
			schema: objectSchema(map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"idle", "running", "waiting_approval"}, "description": "状态过滤（可选）"},
				"limit":  map[string]any{"type": "integer", "description": "返回条数（默认 50，上限 200）"},
				"offset": map[string]any{"type": "integer", "description": "偏移（默认 0）"},
			}),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					Status string `json:"status"`
					Limit  int    `json:"limit"`
					Offset int    `json:"offset"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				limit := defaultInt(params.Limit, 50, remoteAgentListMaxLimit)
				sessions, total, err := s.listRemoteAgentSessions(ctx, remoteAgentListFilter{Status: params.Status, Limit: limit, Offset: params.Offset})
				if err != nil {
					return nil, err
				}
				items := make([]map[string]any, 0, len(sessions))
				for i := range sessions {
					items = append(items, map[string]any{
						"id": sessions[i].ID, "title": sessions[i].Title, "status": sessions[i].Status,
						"mode": sessions[i].Mode, "protocolId": sessions[i].ProtocolID,
						"userTurns": sessions[i].UserTurns, "totalTokens": sessions[i].TotalTokens,
						"pendingActionKind": pendingActionKind(&sessions[i]),
						"updatedAt":         sessions[i].UpdatedAt,
					})
				}
				return map[string]any{"items": items, "total": total}, nil
			},
		},
		{
			name:        "agent_create_session",
			title:       "创建 AI 助手会话",
			description: "创建会话。settings 可预设模型与权限档（allowLiveTest/allowSave/allowDelete: ask|always|never，默认 ask 逐次确认）——自动化场景建议按需放行。编辑模式必须带 protocolId。",
			schema: objectSchema(map[string]any{
				"title":      map[string]any{"type": "string"},
				"mode":       map[string]any{"type": "string", "enum": []string{"create", "edit"}},
				"protocolId": map[string]any{"type": "string"},
				"settings": map[string]any{"type": "object", "description": agentSettingsDoc,
					"properties": agentSettingsSchema()},
			}),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					Title      string          `json:"title"`
					Mode       string          `json:"mode"`
					ProtocolID string          `json:"protocolId"`
					Settings   *agent.Settings `json:"settings"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				session, err := s.createRemoteAgentSession(ctx, params.Title, params.Mode, params.ProtocolID, params.Settings)
				if err != nil {
					return nil, err
				}
				return map[string]any{"sessionId": session.ID, "title": session.Title, "mode": session.Mode}, nil
			},
		},
		{
			name:        "agent_get_session",
			title:       "查看会话详情",
			description: "读取会话详情与全部消息（含历史轮次、方案与草稿）。等待审批时 pendingAction 描述待批内容（凭证脱敏）。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
			}, "sessionId"),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				session, messages, err := s.getRemoteAgentSession(ctx, params.SessionID)
				if err != nil {
					return nil, err
				}
				status := session.Status
				if engine := s.protocolAgentEngine(); engine != nil && engine.IsRunning(session.ID) {
					status = agent.StatusRunning
				}
				msgs := make([]map[string]any, 0, len(messages))
				for _, message := range messages {
					msgs = append(msgs, map[string]any{
						"seq": message.Seq, "role": message.Role, "content": json.RawMessage(message.Content),
						"model": message.Model, "createdAt": message.CreatedAt,
					})
				}
				return map[string]any{
					"sessionId": session.ID, "title": session.Title, "status": status,
					"plan": session.Plan, "draftConfig": json.RawMessage(session.DraftConfig),
					"pendingAction": agent.MaskedPendingAction(session.PendingAction),
					"settings":      session.Settings, "messages": msgs,
				}, nil
			},
		},
		{
			name:        "agent_update_session",
			title:       "修改会话设置",
			description: "修改会话标题或设置（模型选择/思考/计划模式/权限档）。settings 为增量：只传要改的键。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
				"title":     map[string]any{"type": "string"},
				"settings": map[string]any{"type": "object", "description": "增量设置：" + agentSettingsDoc,
					"properties": agentSettingsSchema()},
			}, "sessionId"),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string               `json:"sessionId"`
					Title     *string              `json:"title"`
					Settings  *agent.SettingsPatch `json:"settings"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				session, err := s.updateRemoteAgentSession(ctx, params.SessionID, params.Title, params.Settings)
				if err != nil {
					return nil, err
				}
				return map[string]any{"sessionId": session.ID, "title": session.Title, "settings": session.Settings}, nil
			},
		},
		{
			name:        "agent_delete_session",
			title:       "删除会话",
			description: "删除会话及其全部消息（进行中的轮次先停止）。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
			}, "sessionId"),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				deleted, err := s.deleteRemoteAgentSession(ctx, params.SessionID)
				if err != nil {
					return nil, err
				}
				if !deleted {
					return nil, fmt.Errorf("会话 %q 不存在", params.SessionID)
				}
				return map[string]any{"sessionId": params.SessionID, "deleted": true}, nil
			},
		},
		{
			name:        "agent_send_message",
			title:       "发送消息（驱动一轮）",
			description: "向会话发送用户消息并驱动 AI 助手完整一轮（可能包含多次模型调用与命令执行）。期间推送进度通知；最终返回助手终稿、轮次状态（waiting_approval=有待批动作）、用量。轮次可能持续数分钟。内置助手经 bash 运行 elysia CLI 完成网关管理（模型组/API Key/模型源/协议等）——把运维意图作为消息文本直接下发即可，无需你具备对应工具。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
				"text":      map[string]any{"type": "string", "description": "消息正文"},
				"documents": map[string]any{"type": "array", "description": "附件（可选）", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"}, "mime": map[string]any{"type": "string"},
						"dataUrl": map[string]any{"type": "string", "description": "base64 data URL"},
					}}},
			}, "sessionId", "text"),
			streaming: true,
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string           `json:"sessionId"`
					Text      string           `json:"text"`
					Documents []agent.Document `json:"documents"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				if strings.TrimSpace(params.Text) == "" && len(params.Documents) == 0 {
					return nil, fmt.Errorf("text 与 documents 不能同时为空")
				}
				events, err := s.startRemoteTurn(ctx, params.SessionID, params.Text, params.Documents)
				if err != nil {
					return nil, err
				}
				step := 0
				outcome := collectRemoteTurn(params.SessionID, events, func(event agent.Event) {
					if message := mcpProgressText(event); message != "" {
						step++
						progress(fmt.Sprintf("%s（第 %d 步）", message, step))
					}
				})
				return mcpOutcomeView(outcome), mcpOutcomeError(outcome)
			},
		},
		{
			name:        "agent_respond",
			title:       "处理待批动作",
			description: "处理会话的待批动作：审批（approved=true/false）、回答提问（answer）、确认方案（approved，note 可带修改意见）。会话必须处于 waiting_approval。续跑一轮，期间推送进度，返回同 agent_send_message。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
				"approved":  map[string]any{"type": "boolean", "description": "审批/方案确认的裁决（审批与方案型必填）"},
				"answer":    map[string]any{"type": "string", "description": "提问型暂停的用户作答"},
				"note":      map[string]any{"type": "string", "description": "拒绝/方案修改意见"},
				"apiKey":    map[string]any{"type": "string", "description": "补充测试凭证（可选）"},
				"baseUrl":   map[string]any{"type": "string", "description": "补充测试目标地址（可选）"},
			}, "sessionId"),
			streaming: true,
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string `json:"sessionId"`
					Approved  bool   `json:"approved"`
					Answer    string `json:"answer"`
					Note      string `json:"note"`
					APIKey    string `json:"apiKey"`
					BaseURL   string `json:"baseUrl"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				// 审批型/方案型必须显式给 approved：bool 零值是 false，漏传若
				// 静默放行会把调用方的参数失误变成一次「拒绝」决策。
				if session, _, err := s.getRemoteAgentSession(ctx, params.SessionID); err == nil &&
					session.PendingAction != nil && session.PendingAction.Kind != agent.PendingKindQuestion {
					var probe map[string]json.RawMessage
					if json.Unmarshal(args, &probe) != nil || probe["approved"] == nil {
						return nil, fmt.Errorf("该会话等待审批/方案确认，必须在参数里显式携带 approved（true|false）")
					}
				}
				events, err := s.respondRemoteApproval(ctx, params.SessionID, agent.ApprovalDecision{
					Approved: params.Approved, BaseURL: params.BaseURL, APIKey: params.APIKey,
					Note: params.Note, Answer: params.Answer,
				})
				if err != nil {
					return nil, err
				}
				step := 0
				outcome := collectRemoteTurn(params.SessionID, events, func(event agent.Event) {
					if message := mcpProgressText(event); message != "" {
						step++
						progress(fmt.Sprintf("%s（第 %d 步）", message, step))
					}
				})
				return mcpOutcomeView(outcome), mcpOutcomeError(outcome)
			},
		},
		{
			name:        "agent_stop",
			title:       "停止轮次",
			description: "停止会话中正在进行的轮次。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
			}, "sessionId"),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				stopped := s.stopRemoteTurn(params.SessionID)
				return map[string]any{"sessionId": params.SessionID, "stopped": stopped}, nil
			},
		},
		{
			name:        "agent_clear_messages",
			title:       "清空会话消息",
			description: "清空会话消息（afterSeq>0 时只截断其后消息），保留会话与草稿。会话必须不在运行中。",
			schema: objectSchema(map[string]any{
				"sessionId": map[string]any{"type": "string", "description": "会话 id"},
				"afterSeq":  map[string]any{"type": "integer", "description": "保留 seq <= afterSeq 的消息；0=全清（默认）"},
			}, "sessionId"),
			invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
				var params struct {
					SessionID string `json:"sessionId"`
					AfterSeq  int    `json:"afterSeq"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("参数解析失败: %w", err)
				}
				if params.AfterSeq < 0 {
					return nil, fmt.Errorf("afterSeq 不能为负")
				}
				if s.store == nil {
					return nil, fmt.Errorf("存储不可用")
				}
				if engine := s.protocolAgentEngine(); engine != nil && engine.IsRunning(params.SessionID) {
					return nil, fmt.Errorf("会话轮次进行中，请先 agent_stop")
				}
				if err := s.store.TruncateMessages(ctx, params.SessionID, params.AfterSeq); err != nil {
					return nil, err
				}
				return map[string]any{"sessionId": params.SessionID, "cleared": true}, nil
			},
		},
	}
}

const agentSettingsDoc = `modelSourceId/modelName（模型选择）、thinkingEnabled/thinkingEffort、planMode、allowLiveTest/allowSave/allowDelete（ask|always|never）`

// agentSettingsSchema 生成 settings 对象的 JSON Schema 子集。
func agentSettingsSchema() map[string]any {
	return map[string]any{
		"modelSourceId":   map[string]any{"type": "string"},
		"modelName":       map[string]any{"type": "string"},
		"thinkingEnabled": map[string]any{"type": "boolean"},
		"thinkingEffort":  map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "xhigh", "max", "adaptive"}},
		"planMode":        map[string]any{"type": "boolean"},
		"allowLiveTest":   map[string]any{"type": "string", "enum": []string{"ask", "always", "never"}},
		"allowSave":       map[string]any{"type": "string", "enum": []string{"ask", "always", "never"}},
		"allowDelete":     map[string]any{"type": "string", "enum": []string{"ask", "always", "never"}},
	}
}

// pendingActionKind 取会话待批动作的类型标签（无则空串）。
func pendingActionKind(session *agent.Session) string {
	if session.PendingAction == nil {
		return ""
	}
	return session.PendingAction.Kind
}

// mcpProgressText 把引擎事件折叠为一句进度文本（nil 返回空串表示不发通知）。
func mcpProgressText(event agent.Event) string {
	switch event.Type {
	case agent.EventStatus:
		return event.Text
	case agent.EventToolCall:
		return fmt.Sprintf("调用工具 %s", event.Name)
	case agent.EventToolProgress:
		return fmt.Sprintf("工具 %s 执行中（%dms）", event.Name, event.ElapsedMs)
	case agent.EventToolResult:
		state := "完成"
		if event.Result != nil && !event.Result.OK {
			state = "失败"
		}
		return fmt.Sprintf("工具 %s %s", event.Name, state)
	default:
		return ""
	}
}

// mcpOutcomeView 轮次结果的 structuredContent 视图。
func mcpOutcomeView(outcome remoteTurnOutcome) map[string]any {
	view := map[string]any{
		"sessionId": outcome.SessionID, "status": outcome.Status, "reply": outcome.Reply,
		"rounds": outcome.Rounds, "model": outcome.Model, "durationMs": outcome.DurationMs,
	}
	if outcome.Pending != nil {
		view["pendingAction"] = outcome.Pending
	}
	if outcome.Usage != nil {
		view["usage"] = outcome.Usage
	}
	if outcome.Error != "" {
		view["error"] = outcome.Error
	}
	return view
}

// mcpOutcomeError 轮次失败转为工具级错误（isError=true 的来源）。
func mcpOutcomeError(outcome remoteTurnOutcome) error {
	if outcome.Error != "" {
		return fmt.Errorf("%s", outcome.Error)
	}
	return nil
}

// mcpFindTool 按名取工具定义。
func mcpFindTool(name string) *mcpTool {
	tools := mcpToolset()
	for i := range tools {
		if tools[i].name == name {
			return &tools[i]
		}
	}
	return nil
}
