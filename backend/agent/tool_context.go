package agent

import (
	"context"
	"encoding/json"
	"strings"
)

// ProgressReporter 是 ToolContext 的可选扩展：批处理型工具（如 bash/CLI）
// 逐子步骤执行时上报人类可读进度，引擎把它转成 tool_progress 事件（带
// Text），前端实时展示「正在执行（2/3）：elysia …」。未实现的上下文
// （宿主自定义/测试直调）对工具是静默 no-op。
type ProgressReporter interface {
	ReportProgress(text string)
}

// engineToolContext 是引擎内置的 ToolContext 默认实现：直读会话、草稿
// 写穿透到 Store。
type engineToolContext struct {
	store   Store
	ctx     context.Context
	session *Session
	// progress 逐子步骤进度出口（bash 批内命令）；nil 时 ReportProgress
	// 静默丢弃（测试直调路径）。
	progress func(text string)
}

// WithProgress 返回带进度出口的副本（runOneTool 构造时注入 events）。
func (c *engineToolContext) WithProgress(report func(text string)) *engineToolContext {
	next := *c
	next.progress = report
	return &next
}

// ReportProgress 实现 ProgressReporter：非阻塞尽力送达（缓冲满丢弃，
// 与其他瞬态事件一致——进度丢了只影响实时性，不影响结果）。
func (c *engineToolContext) ReportProgress(text string) {
	if c.progress != nil {
		c.progress(text)
	}
}

// metaFromSession 构造工具上下文视角的会话元数据快照。
func metaFromSession(s *Session) SessionMeta {
	return SessionMeta{
		ID: s.ID, Title: s.Title, Mode: s.Mode, ProtocolID: s.ProtocolID,
		SeedConfig: s.SeedConfig, Draft: s.DraftConfig, Settings: s.Settings,
	}
}

func (c *engineToolContext) SessionMeta() SessionMeta {
	return metaFromSession(c.session)
}

func (c *engineToolContext) Draft() json.RawMessage { return c.session.DraftConfig }

func (c *engineToolContext) SetDraft(draft json.RawMessage) error {
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{DraftConfig: draft}); err != nil {
		return err
	}
	c.session.DraftConfig = append(json.RawMessage(nil), draft...)
	return nil
}

func (c *engineToolContext) TestTarget() (string, string) {
	return c.session.TestBaseURL, c.session.TestAPIKey
}

func (c *engineToolContext) SetTestTarget(baseURL, apiKey string) error {
	update := SessionStateUpdate{}
	if strings.TrimSpace(baseURL) != "" {
		update.TestBaseURL = baseURL
		c.session.TestBaseURL = baseURL
	}
	if strings.TrimSpace(apiKey) != "" {
		update.TestAPIKey = apiKey
		c.session.TestAPIKey = apiKey
	}
	if update.TestBaseURL == "" && update.TestAPIKey == "" {
		return nil
	}
	return c.store.UpdateSessionState(c.ctx, c.session.ID, update)
}

func (c *engineToolContext) SetTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" || title == c.session.Title {
		return nil
	}
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{Title: title}); err != nil {
		return err
	}
	c.session.Title = title
	return nil
}

func (c *engineToolContext) SetPlan(steps []PlanStep) error {
	if steps == nil {
		steps = []PlanStep{}
	}
	if planStepsEqual(c.session.Plan, steps) {
		return nil
	}
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{Plan: steps}); err != nil {
		return err
	}
	c.session.Plan = steps
	return nil
}

func (c *engineToolContext) SetPlanSummary(summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == c.session.PlanSummary {
		return nil
	}
	value := summary
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{PlanSummary: &value}); err != nil {
		return err
	}
	c.session.PlanSummary = summary
	return nil
}

func planStepsEqual(a, b []PlanStep) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Title != b[index].Title || a[index].Status != b[index].Status {
			return false
		}
	}
	return true
}
