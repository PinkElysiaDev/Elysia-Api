package agent

import (
	"context"
	"encoding/json"
	"strings"
)

// engineToolContext 是引擎内置的 ToolContext 默认实现：直读会话、草稿
// 写穿透到 Store。
type engineToolContext struct {
	store   Store
	ctx     context.Context
	session *Session
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
