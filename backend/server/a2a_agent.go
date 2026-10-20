package server

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/agent"
)

type a2aIncomingMessage struct {
	MessageID string
	TaskID    a2a.TaskID
	ContextID string
	Text      string
	Decision  *agentApprovalDecision
}

type agentApprovalDecision struct {
	Approved *bool  `json:"approved"`
	Answer   string `json:"answer"`
	Note     string `json:"note"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl"`
}

// The SDK parses the wire union. This adapter only interprets gateway-specific
// approval data, preserving the difference between missing approved and false.
func parseA2AMessage(message *a2a.Message) (a2aIncomingMessage, error) {
	out := a2aIncomingMessage{MessageID: message.ID, TaskID: message.TaskID, ContextID: message.ContextID}
	var texts []string
	for _, part := range message.Parts {
		if part == nil {
			return out, a2a.ErrInvalidParams
		}
		switch content := part.Content.(type) {
		case a2a.Text:
			if strings.TrimSpace(string(content)) != "" {
				texts = append(texts, string(content))
			}
		case a2a.Data:
			encoded, err := sonic.ConfigStd.Marshal(content.Value)
			if err != nil {
				return out, a2a.ErrInvalidParams
			}
			var decision agentApprovalDecision
			if err := sonic.ConfigStd.Unmarshal(encoded, &decision); err != nil {
				return out, a2a.NewError(a2a.ErrInvalidParams, "invalid approval data")
			}
			out.Decision = &decision
		default:
			return out, a2a.ErrUnsupportedContentType
		}
	}
	out.Text = strings.Join(texts, "\n")
	return out, nil
}

func a2aResumeDecision(session *agent.Session, message a2aIncomingMessage) (agent.ApprovalDecision, error) {
	decision := agent.ApprovalDecision{Note: strings.TrimSpace(message.Text)}
	if data := message.Decision; data != nil {
		if data.Approved != nil {
			decision.Approved = *data.Approved
		}
		decision.Answer, decision.APIKey, decision.BaseURL = data.Answer, data.APIKey, data.BaseURL
		if data.Note != "" {
			decision.Note = data.Note
		}
	}
	if session.PendingAction != nil && session.PendingAction.Kind == agent.PendingKindQuestion {
		if decision.Answer == "" {
			decision.Answer = strings.TrimSpace(message.Text)
		}
		if decision.Answer == "" {
			return decision, fmt.Errorf("提问型任务需要文本作答（或 data part 的 answer 字段）")
		}
		return decision, nil
	}
	if message.Decision == nil || message.Decision.Approved == nil {
		return decision, fmt.Errorf("审批/方案确认必须在 data part 显式携带 {\"approved\": true|false}")
	}
	return decision, nil
}

// The engine owns detached turn lifetime. A disconnected SDK stream stops its
// subscription; this watcher keeps collecting so GetTask/reconnection work.
func (h *a2aService) watch(entry *a2aTaskEntry, events <-chan agent.Event) {
	defer close(entry.roundDone)
	entry.setStatus(a2a.TaskStateWorking, "")
	outcome := collectRemoteTurn(entry.contextID, events, func(event agent.Event) {
		switch event.Type {
		case agent.EventStatus:
			entry.setStatus(a2a.TaskStateWorking, event.Text)
		case agent.EventToolCall:
			entry.setStatus(a2a.TaskStateWorking, fmt.Sprintf("调用工具 %s", event.Name))
		case agent.EventToolResult:
			state := "完成"
			if event.Result != nil && !event.Result.OK {
				state = "失败"
			}
			entry.setStatus(a2a.TaskStateWorking, fmt.Sprintf("工具 %s %s", event.Name, state))
		case agent.EventPlanUpdated:
			entry.setStatus(a2a.TaskStateWorking, "工作方案已更新")
		}
	})
	switch {
	case outcome.Error != "":
		entry.setStatus(a2a.TaskStateFailed, outcome.Error)
	case outcome.Pending != nil:
		entry.setStatus(a2a.TaskStateInputRequired, a2aPendingSummary(outcome.Pending))
	default:
		if outcome.Reply != "" {
			entry.append(&a2a.TaskArtifactUpdateEvent{TaskID: entry.id, ContextID: entry.contextID, LastChunk: true,
				Artifact: &a2a.Artifact{ID: "reply", Name: "assistant-reply", Parts: a2a.ContentParts{
					a2a.NewTextPart(outcome.Reply),
					a2a.NewDataPart(map[string]any{"rounds": outcome.Rounds, "model": outcome.Model, "durationMs": outcome.DurationMs}),
				}}})
		}
		entry.setStatus(a2a.TaskStateCompleted, "")
	}
}

func a2aPendingSummary(pending *agent.PendingAction) string {
	if pending == nil {
		return "等待输入"
	}
	switch pending.Kind {
	case agent.PendingKindQuestion:
		if pending.Question == nil {
			return "等待回答（问题详情缺失）"
		}
		summary := "等待回答：" + pending.Question.Question
		if len(pending.Question.Options) > 0 {
			labels := make([]string, 0, len(pending.Question.Options))
			for _, option := range pending.Question.Options {
				labels = append(labels, option.Label)
			}
			summary += "（选项：" + strings.Join(labels, " / ") + "）"
		}
		return summary
	case agent.PendingKindPlan:
		return fmt.Sprintf("等待方案确认（%d 步）：用 data part {\"approved\": true|false} 回复；拒绝可在文本里附修改意见", len(pending.Plan))
	default:
		names := make([]string, 0, len(pending.Calls))
		for _, call := range pending.Calls {
			names = append(names, call.Name)
		}
		summary := "等待审批"
		if pending.Reason != "" {
			summary += "：" + pending.Reason
		}
		if len(names) > 0 {
			summary += "（工具：" + strings.Join(names, "、") + "）"
		}
		return summary + "；用 data part {\"approved\": true|false} 回复"
	}
}
