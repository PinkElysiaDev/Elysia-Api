package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// AI 助手远程面共享服务层：MCP / A2A 与 /api/agent REST 共用的会话与轮次
// 原语，全部是对引擎（protocolAgentEngine）与存储的薄委托。对外视图复用
// agentSessionView（凭证脱敏）；本层返回引擎/存储结构体，协议面各自编码。

// remoteAgentListMaxLimit 列表分页上限。
const remoteAgentListMaxLimit = 200

// remoteAgentListFilter 会话列表查询参数。
type remoteAgentListFilter struct {
	Status string // 空=全部；idle|running|waiting_approval
	Limit  int    // 0=全量；否则钳到 [1, remoteAgentListMaxLimit]
	Offset int    // 负值按 0 处理
}

// listRemoteAgentSessions 列会话：先叠加 engine.IsRunning 的运行态口径
// （DB 值可能过期），再按状态过滤与分页；返回过滤后总数。
func (s *Server) listRemoteAgentSessions(ctx context.Context, filter remoteAgentListFilter) ([]agent.Session, int, error) {
	if s.store == nil {
		return nil, 0, errors.New("storage unavailable")
	}
	sessions, err := s.store.ListAgentSessions(ctx)
	if err != nil {
		return nil, 0, err
	}
	engine := s.protocolAgentEngine()
	statusFilter := strings.TrimSpace(filter.Status)
	filtered := make([]agent.Session, 0, len(sessions))
	for i := range sessions {
		if engine != nil && engine.IsRunning(sessions[i].ID) {
			sessions[i].Status = agent.StatusRunning
		}
		if statusFilter != "" && sessions[i].Status != statusFilter {
			continue
		}
		filtered = append(filtered, sessions[i])
	}
	total := len(filtered)
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if filter.Limit > 0 {
		if filter.Limit > remoteAgentListMaxLimit {
			filter.Limit = remoteAgentListMaxLimit
		}
		if end = offset + filter.Limit; end > total {
			end = total
		}
	}
	return filtered[offset:end], total, nil
}

// getRemoteAgentSession 会话详情（含消息）。
func (s *Server) getRemoteAgentSession(ctx context.Context, id string) (*agent.Session, []agent.Message, error) {
	if s.store == nil {
		return nil, nil, errors.New("storage unavailable")
	}
	session, err := s.store.GetAgentSession(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.store.ListMessages(ctx, session.ID)
	if err != nil {
		return nil, nil, err
	}
	return session, messages, nil
}

// createRemoteAgentSession 建会话（模式与协议种子校验同管理端点）。
func (s *Server) createRemoteAgentSession(ctx context.Context, title, mode, protocolID string, settings *agent.Settings) (*agent.Session, error) {
	if s.store == nil {
		return nil, errors.New("storage unavailable")
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = agent.ModeCreate
	}
	seed := ""
	protocolID = strings.TrimSpace(protocolID)
	if mode == agent.ModeEdit {
		if protocolID == "" {
			return nil, errors.New("编辑模式必须指定 protocolId")
		}
		rows, err := s.store.ListCustomProtocols(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, row := range rows {
			if strings.EqualFold(row.ID, protocolID) {
				seed, protocolID, found = row.Config, row.ID, true
				break
			}
		}
		if !found {
			return nil, errors.New("protocol not found")
		}
	}
	upsert := storage.AgentSessionUpsert{Title: strings.TrimSpace(title), Mode: mode, ProtocolID: protocolID, SeedConfig: seed}
	if settings != nil {
		upsert.Settings = *settings
	}
	s.inheritRecentSessionSettings(ctx, &upsert.Settings, settings == nil)
	return s.store.CreateAgentSession(ctx, upsert)
}

// inheritRecentSessionSettings 在远程面建会话时继承「最近更新的会话」的
// 模型/思考设置（调用方未显式给出时）。远程客户端（MCP create、A2A 新
// 线程）通常不了解模型配置；用户在 webui 选过一次后，后续远程会话开箱
// 即用，不再撞「模型源 "" 下没有找到模型」。settings 完全缺省
// （inheritAll）时连同权限档一起继承（单用户偏好）；显式传入时只补空缺
// 的模型/思考字段。
func (s *Server) inheritRecentSessionSettings(ctx context.Context, settings *agent.Settings, inheritAll bool) {
	recent, err := s.store.ListAgentSessions(ctx)
	if err != nil || len(recent) == 0 {
		return
	}
	base := recent[0].Settings
	base.TestAPIKeySet = false // 只读凭证标记，新会话没有测试凭证
	// PlanMode 是「本会话当前工作流状态」（等方案确认），不是用户偏好——
	// 继承它会让计划模式缠上之后所有远程新建的会话（写入永远被拒）。
	base.PlanMode = false
	if inheritAll {
		*settings = base
		return
	}
	if settings.ModelSourceID != "" || settings.ModelName != "" {
		return // 调用方显式指定了模型，其余字段尊重传入值
	}
	settings.ModelSourceID = base.ModelSourceID
	settings.ModelName = base.ModelName
	settings.ThinkingEnabled = base.ThinkingEnabled
	settings.ThinkingEffort = base.ThinkingEffort
}

// updateRemoteAgentSession 标题/设置增量更新（思考等级校验同管理端点）。
func (s *Server) updateRemoteAgentSession(ctx context.Context, id string, title *string, settings *agent.SettingsPatch) (*agent.Session, error) {
	if s.store == nil {
		return nil, errors.New("storage unavailable")
	}
	if settings != nil && settings.ThinkingEffort != nil {
		effort := strings.ToLower(strings.TrimSpace(*settings.ThinkingEffort))
		switch effort {
		case "", "low", "medium", "high", "xhigh", "max", "adaptive":
			*settings.ThinkingEffort = effort
		default:
			return nil, fmt.Errorf("thinkingEffort 可选值：low/medium/high/xhigh/max/adaptive")
		}
	}
	return s.store.UpdateAgentSessionSettings(ctx, strings.TrimSpace(id), title, settings, nil, false)
}

// deleteRemoteAgentSession 删除会话（先停轮次）。返回是否存在。
func (s *Server) deleteRemoteAgentSession(ctx context.Context, id string) (bool, error) {
	if s.store == nil {
		return false, errors.New("storage unavailable")
	}
	id = strings.TrimSpace(id)
	if engine := s.protocolAgentEngine(); engine != nil {
		engine.Stop(id)
	}
	return s.store.DeleteAgentSession(ctx, id)
}

// startRemoteTurn 发送用户消息并启动轮次，返回事件流（消费至关闭即轮次终态）。
func (s *Server) startRemoteTurn(ctx context.Context, sessionID, text string, documents []agent.Document) (<-chan agent.Event, error) {
	engine := s.protocolAgentEngine()
	if engine == nil {
		return nil, errors.New("agent engine unavailable")
	}
	return engine.RunTurn(ctx, strings.TrimSpace(sessionID), &agent.UserContent{Text: text, Documents: documents})
}

// respondRemoteApproval 审批/提问作答/方案确认，续跑轮次。
func (s *Server) respondRemoteApproval(ctx context.Context, sessionID string, decision agent.ApprovalDecision) (<-chan agent.Event, error) {
	engine := s.protocolAgentEngine()
	if engine == nil {
		return nil, errors.New("agent engine unavailable")
	}
	return engine.ResumeApproval(ctx, strings.TrimSpace(sessionID), decision)
}

// stopRemoteTurn 停止进行中的轮次。
func (s *Server) stopRemoteTurn(sessionID string) bool {
	engine := s.protocolAgentEngine()
	if engine == nil {
		return false
	}
	return engine.Stop(strings.TrimSpace(sessionID))
}

// remoteTurnErrorInfo 把引擎轮次错误归一化为 (code, message)——A2A 出口
// 使用（MCP 原样透传 error）；状态码与映射表见 agentTurnErrorInfo。
func remoteTurnErrorInfo(err error) (string, string) {
	_, code, message := agentTurnErrorInfo(err)
	return code, message
}

// remoteTurnOutcome 聚合一轮的终态，MCP/A2A 的最终响应都从这里取材。
type remoteTurnOutcome struct {
	SessionID  string
	Status     string               // waiting_approval | idle（完成）| failed
	Reply      string               // 最后一条 assistant 消息文本
	Pending    *agent.PendingAction // 事件出口已脱敏
	Usage      *relay.MaheshvaraUsage
	Model      string
	DurationMs int64
	Rounds     int
	Error      string
	Retryable  bool
}

// remoteTurnStatusFailed 够不上会话状态的失败标记（collectRemoteTurn 产出）。
const remoteTurnStatusFailed = "failed"

// collectRemoteTurn 消费事件流到关闭。onEvent 可为 nil：MCP 用它发 progress
// 通知，A2A 用它映射状态/产物帧——聚合与转发一次遍历完成。
func collectRemoteTurn(sessionID string, events <-chan agent.Event, onEvent func(agent.Event)) remoteTurnOutcome {
	outcome := remoteTurnOutcome{SessionID: sessionID, Status: agent.StatusIdle}
	for event := range events {
		if onEvent != nil {
			onEvent(event)
		}
		switch event.Type {
		case agent.EventMessage:
			// 只记最后一条 assistant 消息：多轮工具循环里每轮模型调用各落
			// 一条，终稿即最后一条。
			if event.Message != nil && event.Message.Role == agent.RoleAssistant {
				var content agent.AssistantContent
				if json.Unmarshal(event.Message.Content, &content) == nil {
					outcome.Reply = content.Text
				}
			}
		case agent.EventApprovalPending:
			outcome.Pending = event.Approval
		case agent.EventTurnDone:
			outcome.Usage = event.Usage
			outcome.Model = event.Model
			outcome.DurationMs = event.DurationMs
			outcome.Rounds = event.Rounds
		case agent.EventError:
			outcome.Error = event.Text
			outcome.Retryable = event.Retryable
		}
	}
	switch {
	case outcome.Error != "":
		outcome.Status = remoteTurnStatusFailed
	case outcome.Pending != nil:
		outcome.Status = agent.StatusWaitingApproval
	}
	return outcome
}
