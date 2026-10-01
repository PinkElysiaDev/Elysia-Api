package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// MaheshvaraStreamRenderer renders protocol-neutral stream events to one of
// the four supported downstream wire protocols.
type MaheshvaraStreamRenderer struct {
	state         *protocol.StreamState
	terminals     map[int]MaheshvaraStreamEvent
	terminalOrder []int
	format        FormatType
	writer        StreamResponseWriter
	responseID    string
	model         string
	createdAt     int64
	usage         *MaheshvaraUsage
	finished      bool
	completed     bool // 完成事件已经成功写出并刷新。
	aborted       bool
	hasOutput     bool

	openAI    *maheshvaraOpenAIRenderState
	claude    *maheshvaraClaudeRenderState
	gemini    *maheshvaraGeminiRenderState
	responses *maheshvaraResponsesRenderState
}

func NewMaheshvaraStreamRenderer(format FormatType, writer StreamResponseWriter, model string) *MaheshvaraStreamRenderer {
	createdAt := time.Now().Unix()
	responseID := newMaheshvaraResponseID("resp")
	renderer := &MaheshvaraStreamRenderer{
		state:      newStreamState(),
		terminals:  make(map[int]MaheshvaraStreamEvent),
		format:     normalizeMaheshvaraStreamFormat(format),
		writer:     writer,
		responseID: responseID,
		model:      model,
		createdAt:  createdAt,
	}
	renderer.openAI = newMaheshvaraOpenAIRenderState()
	renderer.claude = newMaheshvaraClaudeRenderState()
	renderer.gemini = newMaheshvaraGeminiRenderState()
	renderer.responses = newMaheshvaraResponsesRenderState(responseID, model, createdAt)
	return renderer
}

func (renderer *MaheshvaraStreamRenderer) HasOutput() bool {
	return renderer != nil && renderer.hasOutput
}

// Consume defers translated terminals until usage tails have been read. Native
// Responses events retain their original ordering, including post-terminal
// extensions; their wire contract already supplies the final response object.
func (renderer *MaheshvaraStreamRenderer) Consume(event *MaheshvaraStreamEvent) error {
	if event.Type != MaheshvaraEventResponseCompleted || (event.sourceFormat == FormatResponses && renderer.format == FormatResponses) {
		return renderer.Write(event)
	}
	if _, exists := renderer.terminals[event.ChoiceIndex]; exists {
		return nil
	}
	renderer.usage = mergeMaheshvaraStreamUsage(renderer.usage, event.Usage)
	renderer.terminals[event.ChoiceIndex] = *event
	renderer.terminalOrder = append(renderer.terminalOrder, event.ChoiceIndex)
	return nil
}

func (renderer *MaheshvaraStreamRenderer) Write(event *MaheshvaraStreamEvent) error {
	if renderer == nil || event == nil || renderer.finished || renderer.aborted {
		return nil
	}
	if err := validateToolEvent(event, renderer.format); err != nil {
		return err
	}
	if err := validateStreamBatch(renderer.state, []MaheshvaraStreamEvent{*event}, false); err != nil {
		return err
	}
	if event.ResponseID != "" {
		renderer.responseID = event.ResponseID
	}
	if event.Model != "" {
		renderer.model = event.Model
	}
	if event.CreatedAt != 0 {
		renderer.createdAt = event.CreatedAt
	}
	if event.Usage != nil {
		renderer.usage = mergeMaheshvaraStreamUsage(renderer.usage, event.Usage)
	}
	// Replay only parser-identified native events. Internal usage events derived
	// from a completion frame must not replay that frame a second time.
	if event.sourceFormat == FormatResponses && renderer.format == FormatResponses {
		if stringValue(event.Raw["type"]) != event.Type {
			return nil
		}
		if err := renderer.writeSSEEvent(event.Type, event.Raw); err != nil {
			return err
		}
		renderer.responses.started = true
		renderer.hasOutput = renderer.hasOutput || maheshvaraStreamEventHasOutput(*event)
		if event.Type == MaheshvaraEventResponseCompleted {
			renderer.completed = true
			renderer.responses.completed = true
		}
		return nil
	}
	if event.Response != nil && !renderer.hasOutput && len(event.Response.Output) > 0 {
		if err := renderer.writeMaheshvaraResponseContent(event.Response); err != nil {
			return err
		}
	}
	if event.Type == MaheshvaraEventResponseCompleted {
		if err := renderer.state.ValidateComplete(); err != nil {
			return err
		}
	}

	var err error
	switch renderer.format {
	case FormatClaude:
		err = renderer.writeClaude(event)
	case FormatGemini:
		err = renderer.writeGemini(event)
	case FormatResponses:
		err = renderer.writeResponses(event)
	default:
		err = renderer.writeOpenAIChat(event)
	}
	if err == nil {
		err = renderer.checkBuffers()
	}
	if err == nil && maheshvaraStreamEventHasOutput(*event) {
		renderer.hasOutput = true
	}
	if err == nil && event.Type == MaheshvaraEventResponseCompleted {
		renderer.completed = true
	}
	return err
}

func (renderer *MaheshvaraStreamRenderer) WriteResponse(response *MaheshvaraResponse) error {
	if renderer == nil || response == nil {
		return fmt.Errorf("nil Maheshvara stream response")
	}
	if err := validateToolResponse(response, renderer.format); err != nil {
		return err
	}
	if response.Error != nil {
		return renderer.AbortWithError(response.Error)
	}
	if response.ID != "" {
		renderer.responseID = response.ID
	}
	if response.Model != "" {
		renderer.model = response.Model
	}
	if response.CreatedAt != 0 {
		renderer.createdAt = response.CreatedAt
	}
	if err := renderer.writeMaheshvaraResponseContent(response); err != nil {
		return err
	}
	if response.Usage != nil {
		if err := renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventUsageDelta, ResponseID: renderer.responseID, Model: renderer.model, Usage: response.Usage}); err != nil {
			return err
		}
	}
	if response.StopReason != "" || response.Status == "completed" {
		return renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventResponseCompleted, ResponseID: renderer.responseID, Model: renderer.model, FinishReason: response.StopReason, Response: response})
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeMaheshvaraResponseContent(response *MaheshvaraResponse) error {
	for outputIndex := range response.Output {
		item := response.Output[outputIndex]
		if item.Type == "custom_tool_call" || (item.sourceFormat == FormatResponses && item.Type != MaheshvaraOutputMessage && item.Type != MaheshvaraOutputFunctionCall && item.Type != MaheshvaraOutputReasoning) {
			if err := renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventOutputItemDone, OutputIndex: outputIndex, OutputItem: &item}); err != nil {
				return err
			}
			continue
		}
		switch item.Type {
		case MaheshvaraOutputFunctionCall:
			if err := renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name, OutputItem: &item}); err != nil {
				return err
			}
			if len(item.Arguments) > 0 {
				if err := renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallArgumentsDone, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name, ToolArgumentsDone: string(item.Arguments)}); err != nil {
					return err
				}
			}
		case MaheshvaraOutputReasoning:
			text := maheshvaraReasoningText(item)
			if text != "" {
				if err := renderer.Write(&MaheshvaraStreamEvent{Type: MaheshvaraEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ItemID: item.ID, ReasoningDelta: text}); err != nil {
					return err
				}
			}
		default:
			for contentIndex := range item.Content {
				part := item.Content[contentIndex]
				event := &MaheshvaraStreamEvent{ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID}
				switch part.Type {
				case MaheshvaraContentText:
					event.Type = MaheshvaraEventTextDelta
					event.Delta = part.Text
				case MaheshvaraContentReasoning:
					event.Type = MaheshvaraEventReasoningDelta
					event.ReasoningDelta = firstNonEmptyString(part.ReasoningText, part.Text)
				case MaheshvaraContentRefusal:
					event.Type = MaheshvaraEventRefusalDelta
					event.RefusalDelta = part.Text
				default:
					event.Type = MaheshvaraEventContentPartAdded
					event.ContentPart = &part
				}
				if maheshvaraStreamEventHasOutput(*event) {
					if err := renderer.Write(event); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) Finish(ctx context.Context) error {
	if renderer == nil || renderer.finished {
		return nil
	}
	for _, choice := range renderer.terminalOrder {
		event := renderer.terminals[choice]
		event.Usage = nil // Usage was merged on receipt; a tail may supersede it.
		if event.Response != nil {
			response := *event.Response
			response.Usage = renderer.usage
			event.Response = &response
		}
		if err := renderer.Write(&event); err != nil {
			return err
		}
	}
	renderer.terminalOrder = nil
	if err := renderer.state.Finish(); err != nil {
		return err
	}
	var err error
	switch renderer.format {
	case FormatClaude:
		err = renderer.finishClaude()
	case FormatGemini:
		err = renderer.finishGemini()
	case FormatResponses:
		err = renderer.finishResponses()
	default:
		err = renderer.finishOpenAIChat()
	}
	if renderer.completed && errors.Is(ctx.Err(), context.Canceled) {
		err = nil // 客户端已收到终态，取消只会影响收尾标记。
	}
	if err == nil {
		renderer.finished = true
	}
	return err
}

// Abort 以核心错误中止流;携带完整分类/细分码的错误会按客户端线制渲染
// 出对应 type/code(而非一律 upstream_stream_error)。
func (renderer *MaheshvaraStreamRenderer) Abort(streamErr error) error {
	var mErr *MaheshvaraError
	if errors.As(streamErr, &mErr) {
		return renderer.AbortWithError(mErr)
	}
	message := "upstream stream failed"
	if streamErr != nil {
		message = streamErr.Error()
	}
	return renderer.AbortWithError(&MaheshvaraError{Class: ErrorClassUpstream, Message: message})
}

func (renderer *MaheshvaraStreamRenderer) AbortWithError(mErr *MaheshvaraError) error {
	if renderer == nil || renderer.finished || renderer.aborted {
		return nil
	}
	if mErr == nil {
		mErr = &MaheshvaraError{Class: ErrorClassUpstream, Message: "upstream stream failed"}
	}
	mErr.Class = mErr.Class.OrDefault()
	renderer.aborted = true
	var err error
	switch renderer.format {
	case FormatClaude:
		err = renderer.abortClaude(mErr)
	case FormatGemini:
		err = renderer.abortGemini(mErr)
	case FormatResponses:
		err = renderer.abortResponses(mErr)
	default:
		err = renderer.abortOpenAIChat(mErr)
	}
	if err == nil && renderer.writer != nil {
		err = renderer.writer.Flush()
	}
	renderer.finished = true
	return err
}

func TransformStreamViaMaheshvara(ctx context.Context, response *http.Response, sourceFormat, targetFormat FormatType, writer StreamResponseWriter, model string) error {
	if response == nil || response.Body == nil {
		return fmt.Errorf("nil upstream stream response")
	}
	defer response.Body.Close()
	reader := NewSSEEventReader(response.Body)
	defer reader.Close()
	decoder := NewMaheshvaraStreamDecoder(sourceFormat)
	renderer := NewMaheshvaraStreamRenderer(targetFormat, writer, model)

	abort := func(streamErr error) error {
		if renderErr := renderer.Abort(streamErr); renderErr != nil {
			return fmt.Errorf("%w; render stream error: %v", streamErr, renderErr)
		}
		return streamErr
	}
	batchDecoder := &CustomProtocolStreamDecoder{native: decoder}
	if err := readStreamBatches(ctx, reader, batchDecoder, func(_ SSEEvent, events []MaheshvaraStreamEvent, _ bool) error {
		for index := range events {
			event := &events[index]
			if event.Error != nil {
				return event.Error
			}
			if event.Type == MaheshvaraEventResponseFailed {
				return fmt.Errorf("upstream stream failed")
			}
			if err := renderer.Consume(event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return abort(err)
	}
	if !decoder.SawWireEvent() {
		return abort(fmt.Errorf("upstream returned an empty event stream"))
	}
	if !decoder.TerminalReceived() {
		return abort(ErrNoTerminalEvent)
	}
	// 上游发了真实 finish_reason 的空完成（content_filter 拒答、空工具轮等）
	// 是合法响应：只在终态为合成（无 finish 的 [DONE]）时才要求有可表达输出。
	if !decoder.SawOutput() && !renderer.HasOutput() && !decoder.SawFinishReason() {
		return abort(fmt.Errorf("upstream stream completed without representable output"))
	}
	if err := renderer.Finish(ctx); err != nil {
		return abort(err)
	}
	return nil
}
