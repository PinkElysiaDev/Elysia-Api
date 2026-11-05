package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	log "github.com/elysia-api/backend/logging"
)

const answerSummaryRunes = 80

// callGate 是单次工具调用的门禁判定结果。
type callGate int

const (
	gateAllow callGate = iota
	gateDeny
	gatePause
)

// 暂停型待批动作的类别（PendingAction.Kind）。导出供远程面（MCP/A2A/REST）
// 与存储层判别，避免裸串漂移。
const (
	// PendingKindApproval 是经典门控审批（Kind 零值）。
	PendingKindApproval = ""
	// PendingKindQuestion 是 ask_user 提问暂停。
	PendingKindQuestion = "question"
	// PendingKindPlan 是计划模式方案定稿暂停。
	PendingKindPlan = "plan"
)

// ApprovalDecision 是用户对暂停动作的裁决。Answer 用于 ask_user 的作答。
type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	BaseURL  string `json:"baseUrl,omitempty"` // 允许时可补充/更新测试目标
	APIKey   string `json:"apiKey,omitempty"`
	Note     string `json:"note,omitempty"`
	Answer   string `json:"answer,omitempty"`
}

// ResumeApproval 恢复一个等待审批的轮次：执行或拒绝待定动作，然后继续
// 模型循环。
func (e *Engine) ResumeApproval(ctx context.Context, sessionID string, decision ApprovalDecision) (<-chan Event, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != StatusWaitingApproval || !canResumePending(session.PendingAction) {
		return nil, ErrNoPendingApproval
	}
	// 只有批准才真实执行调用：旧版本残留（工具已下线）在批准时明确报
	// 过期；拒绝只合成结果续跑模型，无需注册表，永远放行以便收尾。
	if decision.Approved {
		if err := e.checkPendingTools(session.PendingAction); err != nil {
			return nil, err
		}
	}
	turnCtx, handle, cancel, err := e.beginTurn(sessionID, e.opts.TurnTimeout)
	if err != nil {
		return nil, err
	}
	logger := log.FromContext(ctx).With().Str("session_id", sessionID).Logger()
	return e.spawnTurn(logger.WithContext(turnCtx), sessionID, handle, cancel, nil, session.PendingAction, decision), nil
}

// canResumePending 报告待批动作能否被 ResumeApproval 消费：审批型必须有
// 调用列表；提问型必须带问题（作答要靠 Question.CallID 合成工具结果）；
// 方案型只带步骤清单，Calls 为空是常态。
func canResumePending(pending *PendingAction) bool {
	if pending == nil {
		return false
	}
	switch pending.Kind {
	case PendingKindPlan:
		return true
	case PendingKindQuestion:
		return pending.Question != nil && len(pending.Calls) > 0
	default:
		return len(pending.Calls) > 0
	}
}

// checkPendingTools 校验审批型待批动作里的工具仍在注册表中。跨版本升级
// 会下线旧工具面（如 31 工具收敛为 elysia_cli 后留下的历史审批），此时批准只
// 会把整批调用按未知工具拒绝——对用户呈现为「批准了却失败」，改为明确
// 报过期并提示重新发起。提问/方案型不真实执行调用，无需校验。
func (e *Engine) checkPendingTools(pending *PendingAction) error {
	if pending.Kind != PendingKindApproval {
		return nil
	}
	for _, call := range pending.Calls {
		if e.tools.Get(call.Name) == nil {
			return fmt.Errorf("%w: %s（该审批来自旧版本，请拒绝它并重新发起请求）", ErrStalePending, call.Name)
		}
	}
	return nil
}

// resumeQuestion 把用户作答合成 ask_user 的工具结果；同批其余调用一律合成
// 取消结果——整批 tool_calls 早已持久化在 assistant 消息里，缺任何一个的
// 结果，下一次模型调用都会被上游以配对不完整拒绝（400）。
func (e *Engine) resumeQuestion(ctx context.Context, sessionID string, resume *PendingAction, decision ApprovalDecision, conversation []conversationTurn, events chan Event) ([]conversationTurn, bool, error) {
	answer := strings.TrimSpace(decision.Answer)
	if answer == "" {
		answer = strings.TrimSpace(decision.Note)
	}
	if answer == "" {
		answer = "用户没有作答"
	}
	// canResumePending 已保证 question 型待批必带 Question。
	callID := resume.Question.CallID
	encoded, _ := sonic.ConfigStd.Marshal(map[string]string{"answer": answer})
	info := ToolResultInfo{CallID: callID, Name: ToolNameAskUser, OK: true, Summary: "用户回答：" + truncateRunes(answer, answerSummaryRunes), Data: encoded}
	for _, call := range resume.Calls {
		if call.ID == callID && call.Name == ToolNameAskUser {
			info.Input = call.Arguments
			break
		}
	}
	conversation = e.appendToolResult(ctx, sessionID, conversation, info, e.opts.ToolResultModelLimit, events)
	for _, call := range resume.Calls {
		if call.ID == callID {
			continue
		}
		cancelled := deniedToolResult(call, "用户已回答提问，本批其余调用已取消；如仍需要请在后续步骤重新发起")
		conversation = e.appendToolResult(ctx, sessionID, conversation, cancelled, e.opts.ToolResultModelLimit, events)
	}
	return conversation, false, nil
}

// resumePlan 处理方案定稿：确认关闭计划模式；否则把修改意见交回模型。
func (e *Engine) resumePlan(ctx context.Context, sessionID string, session *Session, decision ApprovalDecision, conversation []conversationTurn, events chan Event) ([]conversationTurn, bool, error) {
	if decision.Approved {
		off := false
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{SettingsPlanMode: &off}); err != nil {
			return conversation, false, fmt.Errorf("关闭计划模式失败: %w", err)
		}
		session.Settings.PlanMode = false
		note := "用户已确认方案，计划模式已关闭。请按方案逐步执行。"
		if _, err := e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "info", Text: note}, "", nil); err != nil {
			return conversation, false, fmt.Errorf("写入方案确认失败: %w", err)
		}
		return append(conversation, textTurn(RoleUser, note)), false, nil
	}
	feedback := strings.TrimSpace(decision.Note)
	if feedback == "" {
		feedback = "用户认为方案需要修改，请先询问具体意见。"
	}
	if _, err := e.store.AppendMessage(ctx, sessionID, RoleUser, UserContent{Text: feedback}, "", nil); err != nil {
		return conversation, false, fmt.Errorf("写入方案反馈失败: %w", err)
	}
	return append(conversation, textTurn(RoleUser, feedback)), false, nil
}

func (e *Engine) resumeApprovalPrefix(ctx context.Context, sessionID string, session *Session, resume *PendingAction, decision ApprovalDecision, conversation []conversationTurn, events chan Event) ([]conversationTurn, bool, error) {
	if resume.Kind == PendingKindQuestion {
		return e.resumeQuestion(ctx, sessionID, resume, decision, conversation, events)
	}
	if resume.Kind == PendingKindPlan {
		return e.resumePlan(ctx, sessionID, session, decision, conversation, events)
	}
	names := make([]string, 0, len(resume.Calls))
	for _, call := range resume.Calls {
		names = append(names, call.Name)
	}
	decisionText := "denied"
	if decision.Approved {
		decisionText = "approved"
	}
	if decision.BaseURL != "" || decision.APIKey != "" {
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{TestBaseURL: decision.BaseURL, TestAPIKey: decision.APIKey}); err != nil {
			return conversation, false, fmt.Errorf("保存测试目标失败: %w", err)
		}
		// The approved tool must use the target and credential just committed.
		if decision.BaseURL != "" {
			session.TestBaseURL = decision.BaseURL
		}
		if decision.APIKey != "" {
			session.TestAPIKey = decision.APIKey
		}
	}
	if _, err := e.store.AppendMessage(ctx, sessionID, RoleApproval, ApprovalContent{Decision: decisionText, Names: names, Note: decision.Note}, "", nil); err != nil {
		return nil, false, fmt.Errorf("写入审批记录失败: %w", err)
	}
	if !decision.Approved {
		// 拒绝：为每个待定调用合成拒绝结果，模型据此改道。
		for _, call := range resume.Calls {
			info := deniedToolResult(call, "用户拒绝了该操作"+denialSuffix(decision.Note))
			conversation = e.appendToolResult(ctx, sessionID, conversation, info, e.opts.ToolResultModelLimit, events)
		}
		return conversation, false, nil
	}
	// 已获用户明确批准：批内调用跳过 ask 级暂停，但 PermissionNever 与
	// 计划模式仍在 executeCalls 内逐调用复核（批准后策略可能已收紧，
	// 例如批量 [test_upstream, save_protocol] 里 save 是 never）。
	approved := make(map[string]bool, len(resume.Calls))
	for _, call := range resume.Calls {
		approved[call.ID] = true
	}
	paused, err := e.executeCalls(ctx, sessionID, session, &conversation, resume.Calls, "", approved, events)
	if err != nil {
		return nil, false, err
	}
	return conversation, paused, nil
}

// pauseForApproval 把 calls[index:] 存为审批型 PendingAction 并暂停轮次。
// elysia_cli 类路由工具的待批子命令清单（pauseNotes）连同同批后续调用的门控
// 命令一并并入 Reason——用户所见即所批（恢复时 approvedIDs 会放行整批）。
func (e *Engine) pauseForApproval(ctx context.Context, sessionID string, session *Session, calls []FunctionCall, index int, reason, pauseNotes string, events chan Event) (bool, error) {
	pendingReason := reason
	if pendingReason != "" && pauseNotes != "" {
		pendingReason += "\n"
	}
	pendingReason += pauseNotes
	for _, later := range calls[index+1:] {
		pendingReason = e.appendProbeNotes(session, later, pendingReason)
	}
	pending := &PendingAction{Calls: append([]FunctionCall(nil), calls[index:]...), Reason: pendingReason}
	waiting := StatusWaitingApproval
	if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
		return false, err
	}
	emitTerminal(ctx, events, Event{Type: EventApprovalPending, Approval: MaskedPendingAction(pending)})
	return true, nil
}

// pauseForPlan 在计划模式方案定稿后暂停，等用户确认或给出修改意见。
func (e *Engine) pauseForPlan(ctx context.Context, sessionID string, session *Session, reason string, events chan Event) error {
	pending := &PendingAction{Kind: PendingKindPlan, Reason: reason, Plan: append([]PlanStep(nil), session.Plan...), PlanSummary: session.PlanSummary}
	waiting := StatusWaitingApproval
	if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
		return fmt.Errorf("保存待确认方案失败: %w", err)
	}
	emitTerminal(ctx, events, Event{Type: EventApprovalPending, Approval: MaskedPendingAction(pending)})
	return nil
}

// pauseForQuestion 把 ask_user 变成 question 型暂停。参数不合法时返回 false，
// 让后续路径按未知或普通工具处理。
func (e *Engine) pauseForQuestion(ctx context.Context, sessionID string, remaining []FunctionCall, reason string, events chan Event) (bool, error) {
	question, ok := e.opts.ParseAsk(remaining[0])
	if !ok {
		return false, nil
	}
	pending := &PendingAction{Kind: PendingKindQuestion, Calls: append([]FunctionCall(nil), remaining...), Reason: reason, Question: &question}
	waiting := StatusWaitingApproval
	if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
		return false, fmt.Errorf("保存待回答问题失败: %w", err)
	}
	emitTerminal(ctx, events, Event{Type: EventApprovalPending, Approval: MaskedPendingAction(pending)})
	return true, nil
}

// gateCall 复核单个调用的门禁（计划模式 → 显式禁令 → ask 级审批），返回
// 判定、拒绝文案与审批卡补充说明。批准集命中只豁免 ask；never 与计划模式
// 始终逐调用复核——批准后策略可能已收紧。
func (e *Engine) gateCall(session *Session, call FunctionCall, tool Tool, approvedIDs map[string]bool) (gate callGate, denial, pauseNotes string) {
	if !tool.Gated() {
		// 路由型工具（如 elysia_cli）自身不门控：按解析出的子命令判定。探针
		// 未实现或解析失败（ok=false）时放行——安全性由执行阶段对同一
		// 解析器的失败兜底保证（见 elysiaCLITool.ProbeGates 注释）。
		if notes, probed := probeGatesFor(tool, call); probed {
			return e.gateProbeDecision(session, call, notes, approvedIDs)
		}
		return gateAllow, "", ""
	}
	return permissionGate(session, tool.PermissionKey(), call.ID, approvedIDs)
}

// permissionGate 是单权限键的判定阶梯：计划模式 → never → ask。
func permissionGate(session *Session, key, callID string, approvedIDs map[string]bool) (callGate, string, string) {
	if session.Settings.PlanMode {
		return gateDeny, "计划模式已开启：" + permissionKeyLabel(key) + " 类操作暂不执行。只读查询照常可用；修改类动作请先用 update_plan 给出完整方案，用户确认后系统会关闭计划模式再执行", ""
	}
	switch PermissionFor(session.Settings, key) {
	case PermissionNever:
		return gateDeny, "用户已在会话设置中禁止此操作，请改用其他方式完成任务", ""
	case PermissionAsk:
		if !approvedIDs[callID] {
			return gatePause, "", ""
		}
	}
	return gateAllow, "", ""
}

// formatGateNote 把一条待批命令渲染为审批卡行（命令 + 权限档）。
func formatGateNote(note GateNote) string {
	return note.Command + "（权限档：" + permissionKeyLabel(note.PermissionKey) + "）"
}

// permissionKeyLabel 给内部权限键配中文释义——审批卡模型与用户同见，
// 裸键名（save/live_test/delete）读不出含义。
func permissionKeyLabel(key string) string {
	switch key {
	case PermissionKeySave:
		return "save=写入"
	case PermissionKeyLiveTest:
		return "live_test=真实出站"
	case PermissionKeyDelete:
		return "delete=删除"
	}
	return key
}

// probeGatesFor 取路由型工具的探针结果；未实现 GateProbe 或解析失败
// （ok=false）时 probed=false，调用方按放行处理。
func probeGatesFor(tool Tool, call FunctionCall) (notes []GateNote, probed bool) {
	probe, implements := tool.(GateProbe)
	if !implements {
		return nil, false
	}
	notes, probed = probe.ProbeGates(call.Arguments)
	return notes, probed
}

// gateProbeDecision 聚合探针上报的子命令权限：任一 never 拒绝并点名
// 全部命中命令；任一 ask 且未批准则暂停（notes 作为审批卡补充说明）。
func (e *Engine) gateProbeDecision(session *Session, call FunctionCall, notes []GateNote, approvedIDs map[string]bool) (callGate, string, string) {
	if len(notes) == 0 {
		return gateAllow, "", ""
	}
	if session.Settings.PlanMode {
		// 点名批内被拒命令并说明只读可拆分重发——否则模型把「整批零
		// 输出」泛化成「没有这个能力」，探索调查都不敢做。
		blocked := make([]string, 0, len(notes))
		for _, note := range notes {
			blocked = append(blocked, formatGateNote(note))
		}
		return gateDeny, "计划模式已开启，本批暂不执行。批内含修改/出站/删除命令：\n" +
			strings.Join(blocked, "\n") +
			"\n批内只读命令也被连带跳过了——把只读查询（ls / help / usage 等）单独重发即可执行；" +
			"修改类动作请先用 update_plan 给出完整方案，用户确认后系统会关闭计划模式再执行", ""
	}
	var pauseNotes, deniedNotes []string
	for _, note := range notes {
		switch PermissionFor(session.Settings, note.PermissionKey) {
		case PermissionNever:
			deniedNotes = append(deniedNotes, formatGateNote(note))
		case PermissionAsk:
			if !approvedIDs[call.ID] {
				pauseNotes = append(pauseNotes, formatGateNote(note))
			}
		}
	}
	if len(deniedNotes) > 0 {
		return gateDeny, "以下命令需要用户已禁止的权限，请调整方案：\n" + strings.Join(deniedNotes, "\n"), ""
	}
	if len(pauseNotes) > 0 {
		return gatePause, "", "待批命令：\n" + strings.Join(pauseNotes, "\n")
	}
	return gateAllow, "", ""
}

// appendProbeNotes 把同批后续路由型调用（elysia_cli）的待批命令并入审批说明。
// 只列 ask 级：never 级在恢复执行时会被逐调用拒绝，不属于用户批准面。
// 只在新轮次的暂停路径被调用（此时门控判定尚未批准任何调用）。
func (e *Engine) appendProbeNotes(session *Session, call FunctionCall, reason string) string {
	tool := e.tools.Get(call.Name)
	if tool == nil || tool.Gated() {
		return reason
	}
	notes, probed := probeGatesFor(tool, call)
	if !probed {
		return reason
	}
	for _, note := range notes {
		if PermissionFor(session.Settings, note.PermissionKey) == PermissionAsk {
			if reason != "" {
				reason += "\n"
			}
			reason += formatGateNote(note)
		}
	}
	return reason
}

// PermissionFor 取权限键的策略（未知键视为 ask）。
func PermissionFor(s Settings, key string) string {
	// 权限值在存储写入与读取（遗留行兼容）时已归一化，此处直接消费。
	// 键集合与 KnownPermissionKey 同源：未知键按 ask 只是运行时兜底，
	// 注册表装配时已拒绝这类工具。
	switch key {
	case PermissionKeyLiveTest:
		return s.AllowLiveTest
	case PermissionKeySave:
		return s.AllowSave
	case PermissionKeyDelete:
		return s.AllowDelete
	default:
		return PermissionAsk
	}
}

func denialSuffix(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return "（备注：" + note + "）"
}
