package relay

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// MaheshvaraStreamRenderer renders protocol-neutral stream events to one of
// the four supported downstream wire protocols.
type MaheshvaraStreamRenderer struct {
	format     FormatType
	writer     StreamResponseWriter
	responseID string
	model      string
	createdAt  int64
	usage      *CanonicalUsage
	finished   bool
	aborted    bool
	hasOutput  bool

	openAI    *maheshvaraOpenAIRenderState
	claude    *maheshvaraClaudeRenderState
	gemini    *maheshvaraGeminiRenderState
	responses *maheshvaraResponsesRenderState
}

func NewMaheshvaraStreamRenderer(format FormatType, writer StreamResponseWriter, model string) *MaheshvaraStreamRenderer {
	createdAt := time.Now().Unix()
	responseID := newCanonicalResponseID("resp")
	renderer := &MaheshvaraStreamRenderer{
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

func (renderer *MaheshvaraStreamRenderer) Write(event *MaheshvaraStreamEvent) error {
	if renderer == nil || event == nil || renderer.finished || renderer.aborted {
		return nil
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
		renderer.usage = mergeCanonicalStreamUsage(renderer.usage, event.Usage)
	}
	if event.Response != nil && !renderer.hasOutput && len(event.Response.Output) > 0 {
		if err := renderer.writeCanonicalResponseContent(event.Response); err != nil {
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
	if err == nil && maheshvaraStreamEventHasOutput(*event) {
		renderer.hasOutput = true
	}
	return err
}

func (renderer *MaheshvaraStreamRenderer) WriteResponse(response *CanonicalResponse) error {
	if renderer == nil || response == nil {
		return fmt.Errorf("nil Maheshvara stream response")
	}
	if response.Error != nil {
		return renderer.Abort(fmt.Errorf("%s", response.Error.Message))
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
	if err := renderer.writeCanonicalResponseContent(response); err != nil {
		return err
	}
	if response.Usage != nil {
		if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventUsageDelta, ResponseID: renderer.responseID, Model: renderer.model, Usage: response.Usage}); err != nil {
			return err
		}
	}
	if response.StopReason != "" || response.Status == "completed" {
		return renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventResponseCompleted, ResponseID: renderer.responseID, Model: renderer.model, FinishReason: response.StopReason, Response: response})
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeCanonicalResponseContent(response *CanonicalResponse) error {
	for outputIndex := range response.Output {
		item := response.Output[outputIndex]
		switch item.Type {
		case CanonicalOutputFunctionCall:
			if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name, OutputItem: &item}); err != nil {
				return err
			}
			if len(item.Arguments) > 0 {
				if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallArgumentsDone, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name, ToolArgumentsDone: string(item.Arguments)}); err != nil {
					return err
				}
			}
		case CanonicalOutputReasoning:
			text := canonicalReasoningText(item)
			if text != "" {
				if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ItemID: item.ID, ReasoningDelta: text}); err != nil {
					return err
				}
			}
		default:
			for contentIndex := range item.Content {
				part := item.Content[contentIndex]
				event := &MaheshvaraStreamEvent{ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID}
				switch part.Type {
				case CanonicalContentText:
					event.Type = CanonicalEventTextDelta
					event.Delta = part.Text
				case CanonicalContentReasoning:
					event.Type = CanonicalEventReasoningDelta
					event.ReasoningDelta = firstNonEmptyString(part.ReasoningText, part.Text)
				case CanonicalContentRefusal:
					event.Type = CanonicalEventRefusalDelta
					event.RefusalDelta = part.Text
				default:
					event.Type = CanonicalEventContentPartAdded
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

func (renderer *MaheshvaraStreamRenderer) Finish() error {
	if renderer == nil || renderer.finished {
		return nil
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
	if err == nil {
		renderer.finished = true
		if renderer.writer != nil {
			err = renderer.writer.Flush()
		}
	}
	return err
}

func (renderer *MaheshvaraStreamRenderer) Abort(streamErr error) error {
	if renderer == nil || renderer.finished || renderer.aborted {
		return nil
	}
	if streamErr == nil {
		streamErr = fmt.Errorf("upstream stream failed")
	}
	renderer.aborted = true
	var err error
	switch renderer.format {
	case FormatClaude:
		err = renderer.abortClaude(streamErr)
	case FormatGemini:
		err = renderer.abortGemini(streamErr)
	case FormatResponses:
		err = renderer.abortResponses(streamErr)
	default:
		err = renderer.abortOpenAIChat(streamErr)
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
	var terminalEvents []MaheshvaraStreamEvent

	abort := func(streamErr error) error {
		if renderErr := renderer.Abort(streamErr); renderErr != nil {
			return fmt.Errorf("%w; render stream error: %v", streamErr, renderErr)
		}
		return streamErr
	}
	for {
		wireEvent, ok, err := reader.Read(ctx, DefaultSSEIdleTimeout)
		if err != nil {
			return abort(err)
		}
		if !ok {
			break
		}
		events, err := decoder.Decode(wireEvent)
		if err != nil {
			return abort(err)
		}
		for index := range events {
			event := events[index]
			if event.Type == CanonicalEventResponseFailed || event.Error != nil {
				message := "upstream stream failed"
				if event.Error != nil && event.Error.Message != "" {
					message = event.Error.Message
				}
				return abort(fmt.Errorf("%s", message))
			}
			if event.Type == CanonicalEventResponseCompleted {
				terminalEvents = append(terminalEvents, event)
				continue
			}
			if err := renderer.Write(&event); err != nil {
				return abort(err)
			}
		}
	}
	if !decoder.SawWireEvent() {
		return abort(fmt.Errorf("upstream returned an empty event stream"))
	}
	if !decoder.TerminalReceived() {
		return abort(fmt.Errorf("upstream stream ended before a terminal event"))
	}
	if !decoder.SawOutput() && !renderer.HasOutput() {
		return abort(fmt.Errorf("upstream stream completed without representable output"))
	}
	for index := range terminalEvents {
		if err := renderer.Write(&terminalEvents[index]); err != nil {
			return abort(err)
		}
	}
	return renderer.Finish()
}

func mergeCanonicalStreamUsage(current, update *CanonicalUsage) *CanonicalUsage {
	if update == nil {
		return current
	}
	if current == nil {
		copy := *update
		return &copy
	}
	mergeInt := func(target *int, value int) {
		if value != 0 {
			*target = value
		}
	}
	mergeInt(&current.InputTokens, update.InputTokens)
	mergeInt(&current.OutputTokens, update.OutputTokens)
	mergeInt(&current.TotalTokens, update.TotalTokens)
	mergeInt(&current.CachedInputTokens, update.CachedInputTokens)
	mergeInt(&current.CacheCreationInputTokens, update.CacheCreationInputTokens)
	mergeInt(&current.ReasoningTokens, update.ReasoningTokens)
	mergeInt(&current.AcceptedPredictionTokens, update.AcceptedPredictionTokens)
	mergeInt(&current.RejectedPredictionTokens, update.RejectedPredictionTokens)
	if current.TotalTokens == 0 {
		current.TotalTokens = current.InputTokens + current.OutputTokens
	}
	if update.Source != "" {
		current.Source = update.Source
	}
	if update.Provider != "" {
		current.Provider = update.Provider
	}
	return current
}
