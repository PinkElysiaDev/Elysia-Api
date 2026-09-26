package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// 上下文压缩的阈值与长度锚点。
const (
	summaryMinTokens     = 8_000 // 摘要的最小对话规模：更短的对话即使比例高也不值得
	summaryMinMessages   = 8     // 同上：消息条数下限
	summaryMinCut        = 2     // 切点下界：至少摘要这么多条
	summaryKeepTail      = 4     // 切点上界之外至少保留这么多条
	microKeepToolResults = 4     // 微压缩保留最近几条工具结果
	summaryHeadTextLimit = 1500  // 摘要输入里单条消息的字符上限
)

// prepareContext 在每次模型调用前估算水位。超过微压缩线时把较早的工具结果
// 换成占位符（只改本次发送副本，库里原文不动）。摘要压缩不在轮内做——
// 见 maybeSummarize。
func (e *Engine) prepareContext(conversation []relay.MaheshvaraMessage, events chan Event) []relay.MaheshvaraMessage {
	window := e.opts.ContextWindowTokens
	before := estimateTokens(conversation)
	e.emitContext(events, before, window)

	if float64(before) < microCompactRatio*float64(window) {
		return conversation
	}
	compacted, cleared := microCompact(conversation)
	if cleared > 0 {
		emitEvent(events, Event{Type: EventContextCompacted, Compaction: &Compaction{Kind: "micro", Summarized: cleared, Kept: len(compacted) - cleared, BeforeTokens: before}})
		return compacted
	}
	return conversation
}

// maybeSummarize 在轮次开始时（对话刚从库里加载、消息 seq 仍与元素一一对
// 应时）决定是否做摘要压缩。摘要只覆盖切点之前的历史，boundary 记被摘要
// 前缀最后一条消息的真实 seq——下一轮回放按它丢弃已摘要原文、保留其余。
func (e *Engine) maybeSummarize(ctx context.Context, sessionID string, session *Session, conversation []relay.MaheshvaraMessage, seqs []int, events chan Event) []relay.MaheshvaraMessage {
	window := e.opts.ContextWindowTokens
	before := estimateTokens(conversation)
	// 摘要有额外模型调用成本，短对话即使比例高也不值得。
	if float64(before) < summaryCompactRatio*float64(window) || before < summaryMinTokens || len(conversation) < summaryMinMessages {
		return conversation
	}
	cut := summaryCut(conversation)
	if cut < 0 {
		return conversation
	}
	summary, err := e.summarizeHead(ctx, session, conversation[:cut])
	if err != nil || summary == "" {
		return conversation
	}
	if _, err := e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "summary", Text: summary, BoundarySeq: seqs[cut-1]}, "", nil); err != nil {
		return conversation
	}
	head := relay.MaheshvaraMessage{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "以下是此前对话的摘要，请据此继续：\n" + summary}}}
	next := append([]relay.MaheshvaraMessage{head}, conversation[cut:]...)
	emitEvent(events, Event{Type: EventContextCompacted, Compaction: &Compaction{
		Kind: "summary", Summarized: cut, Kept: len(conversation) - cut, BeforeTokens: before, AfterTokens: estimateTokens(next),
	}})
	e.emitContext(events, estimateTokens(next), window)
	return next
}

// summaryCut 找摘要切点：从中点向前回退到最近的 user 消息边界。保留段以
// user 开头才能保证 assistant 的 tool_calls 与其 tool 结果整批留在一起——
// 从批中间切开会产生孤儿 tool 结果，下一次模型调用直接被上游 400。
// 扇入区间 [2, len-4]：至少摘要 2 条、保留 4 条；找不到边界返回 -1（本轮
// 放弃摘要，微压缩照常兜底）。
func summaryCut(conversation []relay.MaheshvaraMessage) int {
	maxCut := len(conversation) - summaryKeepTail
	if maxCut < summaryMinCut {
		return -1
	}
	center := len(conversation) / 2
	if center > maxCut {
		center = maxCut
	}
	for index := center; index >= summaryMinCut; index-- {
		if conversation[index].Role == "user" {
			return index
		}
	}
	return -1
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
	if len(toolIndexes) <= microKeepToolResults {
		return conversation, 0
	}
	drop := map[int]bool{}
	for _, index := range toolIndexes[:len(toolIndexes)-microKeepToolResults] {
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
		if part.ToolOutput == "" || strings.Contains(part.ToolOutput, "旧命令结果已清除") {
			continue
		}
		part.ToolOutput = `{"note":"[旧命令结果已清除，需要时请重新运行]"}`
		out[index].Content = []relay.MaheshvaraContentPart{part}
		cleared++
	}
	return out, cleared
}

// summarizeHead 把给定前缀交给模型生成结构化摘要（失败重试，最终由调用方
// 降级放弃）。
func (e *Engine) summarizeHead(ctx context.Context, session *Session, head []relay.MaheshvaraMessage) (string, error) {
	if len(head) == 0 {
		return "", fmt.Errorf("empty head")
	}
	var b strings.Builder
	for _, message := range head {
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
			return strings.TrimSpace(result.Text), nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("empty summary")
	}
	return "", last
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
	// rune 截断：按字节切会把中文摘要前缀切成非法 UTF-8 进提示词。
	return truncateRunes(strings.Join(parts, " "), summaryHeadTextLimit)
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
