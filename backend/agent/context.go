package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// prepareContext 在每次模型调用前估算水位。超过微压缩线时把较早的工具结果
// 换成占位符（只改本次发送副本，库里原文不动）；超过摘要线且历史够长时，
// 调模型把最早若干轮收成一条摘要消息并落库。
func (e *Engine) prepareContext(ctx context.Context, sessionID string, session *Session, conversation []relay.MaheshvaraMessage, events chan Event) []relay.MaheshvaraMessage {
	window := e.opts.ContextWindowTokens
	before := estimateTokens(conversation)
	e.emitContext(events, before, window)

	if float64(before) < microCompactRatio*float64(window) {
		return conversation
	}
	compacted, cleared := microCompact(conversation)
	if cleared > 0 {
		emitEvent(events, Event{Type: EventContextCompacted, Compaction: &Compaction{Kind: "micro", Summarized: cleared, Kept: len(compacted) - cleared, BeforeTokens: before}})
		conversation = compacted
		before = estimateTokens(conversation)
	}
	// 摘要有额外模型调用成本，短对话即使比例高也不值得。8k token 是下限。
	if float64(before) < summaryCompactRatio*float64(window) || before < 8_000 || len(conversation) < 8 {
		return conversation
	}
	summary, kept, summarized, err := e.summarizeHead(ctx, session, conversation)
	if err != nil || summary == "" {
		return conversation
	}
	messages, err := e.store.ListMessages(ctx, sessionID)
	if err != nil {
		return conversation
	}
	boundary := 0
	if len(messages) > 0 {
		boundary = messages[len(messages)-1].Seq
	}
	if _, err := e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "summary", Text: summary, BoundarySeq: boundary}, "", nil); err != nil {
		return conversation
	}
	head := relay.MaheshvaraMessage{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "以下是此前对话的摘要，请据此继续：\n" + summary}}}
	next := append([]relay.MaheshvaraMessage{head}, kept...)
	emitEvent(events, Event{Type: EventContextCompacted, Compaction: &Compaction{
		Kind: "summary", Summarized: summarized, Kept: len(kept), BeforeTokens: before, AfterTokens: estimateTokens(next),
	}})
	e.emitContext(events, estimateTokens(next), window)
	return next
}

func (e *Engine) emitContext(events chan Event, tokens, window int) {
	ratio := 0.0
	if window > 0 {
		ratio = float64(tokens) / float64(window)
	}
	emitEvent(events, Event{Type: EventContextUpdated, Context: &ContextUsage{InputTokens: tokens, WindowTokens: window, Ratio: ratio}})
}

// microCompact 保留最近 4 条工具结果，更早的 Data 换成占位说明。
func microCompact(conversation []relay.MaheshvaraMessage) ([]relay.MaheshvaraMessage, int) {
	toolIndexes := make([]int, 0)
	for index, message := range conversation {
		if message.Role == "tool" {
			toolIndexes = append(toolIndexes, index)
		}
	}
	if len(toolIndexes) <= 4 {
		return conversation, 0
	}
	drop := map[int]bool{}
	for _, index := range toolIndexes[:len(toolIndexes)-4] {
		drop[index] = true
	}
	cleared := 0
	out := make([]relay.MaheshvaraMessage, len(conversation))
	for index, message := range conversation {
		out[index] = message
		if !drop[index] || len(message.Content) == 0 {
			continue
		}
		part := message.Content[0]
		if part.ToolOutput == "" || strings.Contains(part.ToolOutput, "旧工具结果已清除") {
			continue
		}
		part.ToolOutput = `{"note":"[旧工具结果已清除，需要时请重新调用]"}`
		out[index].Content = []relay.MaheshvaraContentPart{part}
		cleared++
	}
	return out, cleared
}

// summarizeHead 把最早一半对话交给模型摘要，保留后半。失败由调用方降级。
func (e *Engine) summarizeHead(ctx context.Context, session *Session, conversation []relay.MaheshvaraMessage) (string, []relay.MaheshvaraMessage, int, error) {
	cut := len(conversation) / 2
	if cut < 2 {
		return "", conversation, 0, nil
	}
	var b strings.Builder
	for _, message := range conversation[:cut] {
		b.WriteString(message.Role)
		b.WriteString(": ")
		b.WriteString(messageText(message))
		b.WriteByte('\n')
	}
	req := CallRequest{
		Model:         session.Settings.ModelName,
		ModelSourceID: session.Settings.ModelSourceID,
		Instructions:  "把下面的对话压缩成结构化摘要，保留：目标、已定决策、关键数据、未决事项。不要寒暄。",
		Messages:      []relay.MaheshvaraMessage{{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: b.String()}}}},
	}
	var last error
	for attempt := 0; attempt <= compactionRetries; attempt++ {
		result, err := e.caller.Call(ctx, req, StreamCallbacks{})
		if err == nil && result != nil && strings.TrimSpace(result.Text) != "" {
			return strings.TrimSpace(result.Text), conversation[cut:], cut, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("empty summary")
	}
	return "", nil, 0, last
}

func messageText(message relay.MaheshvaraMessage) string {
	var parts []string
	for _, part := range message.Content {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
		if part.ToolOutput != "" {
			parts = append(parts, part.ToolOutput)
		}
	}
	text := strings.Join(parts, " ")
	if len(text) > 1500 {
		return text[:1500]
	}
	return text
}

// applySummaryBoundary 丢弃最近一条摘要所覆盖的历史，并把摘要本身变成
// 一条用户消息。没有摘要时原样返回。
func applySummaryBoundary(messages []Message) []Message {
	boundary := -1
	var summary SystemContent
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != RoleSystem {
			continue
		}
		var content SystemContent
		if err := json.Unmarshal(messages[index].Content, &content); err != nil || content.Kind != "summary" {
			continue
		}
		boundary = content.BoundarySeq
		summary = content
		break
	}
	if boundary < 0 {
		return messages
	}
	kept := make([]Message, 0, len(messages))
	encoded, _ := json.Marshal(UserContent{Text: "以下是此前对话的摘要，请据此继续：\n" + summary.Text})
	kept = append(kept, Message{Role: RoleUser, Content: encoded})
	for _, message := range messages {
		if message.Seq > boundary {
			kept = append(kept, message)
		}
	}
	return kept
}

// estimateTokens 用字符数粗估 token（中英混合按 3 字一个 token）。只用于
// 决定是否压缩，不用于计费。
func estimateTokens(conversation []relay.MaheshvaraMessage) int {
	encoded, err := json.Marshal(conversation)
	if err != nil {
		return 0
	}
	tokens := len(encoded) / 3
	if tokens < 1 && len(encoded) > 0 {
		return 1
	}
	return tokens
}
