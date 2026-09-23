package relay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// maheshvaraResponsesPartState 是消息上一类内容槽（output_text 或 refusal）
// 的流式状态。text/refusal 两槽形状完全同构，差异只在 part 与事件名。
type maheshvaraResponsesPartState struct {
	index   int
	started bool
	done    bool
	text    strings.Builder
}

// 消息内容槽下标。
const (
	responsesPartText = iota
	responsesPartRefusal
)

// responsesPartMeta 描述一个内容槽的线制形状。
type responsesPartMeta struct {
	partType string // ContentPartAdded 的 part.type
	eventKey string // part 里取增量文本的键（text / refusal）
	delta    string // 增量事件名
	done     string // 完成事件名
}

var responsesPartMetas = map[int]responsesPartMeta{
	responsesPartText:    {partType: "output_text", eventKey: "text", delta: MaheshvaraEventTextDelta, done: MaheshvaraEventTextDone},
	responsesPartRefusal: {partType: "refusal", eventKey: "refusal", delta: MaheshvaraEventRefusalDelta, done: MaheshvaraEventRefusalDone},
}

type maheshvaraResponsesMessageState struct {
	id          string
	outputIndex int
	parts       [2]maheshvaraResponsesPartState
	extraParts  map[int]any
	done        bool
}

// slot 返回指定内容槽的便捷引用。
func (s *maheshvaraResponsesMessageState) slot(part int) *maheshvaraResponsesPartState {
	return &s.parts[part]
}

type maheshvaraResponsesReasoningState struct {
	id          string
	outputIndex int
	text        strings.Builder
	signature   strings.Builder
	encrypted   string
	done        bool
}

type maheshvaraResponsesToolState struct {
	id          string
	callID      string
	name        string
	outputIndex int
	arguments   strings.Builder
	added       bool
	done        bool
}

type maheshvaraResponsesRenderState struct {
	started    bool
	completed  bool
	sequence   int64
	responseID string
	model      string
	createdAt  int64
	nextOutput int
	messages   map[int]*maheshvaraResponsesMessageState
	reasoning  map[int]*maheshvaraResponsesReasoningState
	tools      map[string]*maheshvaraResponsesToolState
	toolOrder  []string
}

func newMaheshvaraResponsesRenderState(responseID, model string, createdAt int64) *maheshvaraResponsesRenderState {
	return &maheshvaraResponsesRenderState{
		responseID: responseID,
		model:      model,
		createdAt:  createdAt,
		messages:   make(map[int]*maheshvaraResponsesMessageState),
		reasoning:  make(map[int]*maheshvaraResponsesReasoningState),
		tools:      make(map[string]*maheshvaraResponsesToolState),
	}
}

func (renderer *MaheshvaraStreamRenderer) writeResponses(event *MaheshvaraStreamEvent) error {
	if event == nil {
		return nil
	}
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	switch event.Type {
	case MaheshvaraEventResponseCreated, MaheshvaraEventResponseInProgress, MaheshvaraEventUsageDelta:
		return nil
	case MaheshvaraEventTextDelta:
		return renderer.writeResponsesText(event.ChoiceIndex, event.Delta)
	case MaheshvaraEventTextDone:
		return renderer.finishResponsesText(event.ChoiceIndex, event.TextDone)
	case MaheshvaraEventRefusalDelta:
		return renderer.writeResponsesRefusal(event.ChoiceIndex, event.RefusalDelta)
	case MaheshvaraEventRefusalDone:
		return renderer.finishResponsesRefusal(event.ChoiceIndex, event.RefusalDone)
	case MaheshvaraEventReasoningDelta, MaheshvaraEventReasoningSummaryDelta:
		return renderer.writeResponsesReasoning(event.ChoiceIndex, event.ReasoningDelta)
	case MaheshvaraEventReasoningDone, MaheshvaraEventReasoningSummaryDone:
		return renderer.finishResponsesReasoning(event.ChoiceIndex, event.ReasoningDone)
	case MaheshvaraEventReasoningSignatureDelta:
		return renderer.writeResponsesReasoningSignature(event.ChoiceIndex, event.ReasoningSignatureDelta)
	case MaheshvaraEventAnnotationDelta:
		// 引用标注并入当前文本 part 的 annotations（不生成畸形独立 part）。
		if len(event.Annotations) == 0 {
			return nil
		}
		if state := renderer.responses.messages[event.ChoiceIndex]; state != nil && state.slot(responsesPartText).started {
			if part, ok := state.extraParts[state.slot(responsesPartText).index].(map[string]any); ok {
				annotations, _ := part["annotations"].([]any)
				for _, citation := range event.Annotations {
					annotations = append(annotations, citation)
				}
				part["annotations"] = annotations
			}
		}
		return nil
	case MaheshvaraEventContentPartAdded:
		return renderer.writeResponsesContentPart(event)
	case MaheshvaraEventFunctionCallAdded, MaheshvaraEventFunctionCallArgumentsDelta, MaheshvaraEventFunctionCallArgumentsDone:
		return renderer.writeResponsesTool(event)
	case MaheshvaraEventOutputItemAdded, MaheshvaraEventOutputItemDone:
		// done 事件携带完整终态 item（推理密文、已完成工具调用等只在此出现），
		// 与 added 同路径处理；各 finish 路径幂等，不会重复输出。
		return renderer.writeResponsesOutputItem(event)
	case MaheshvaraEventResponseCompleted:
		return renderer.completeResponses()
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) ensureResponsesHeader() error {
	state := renderer.responses
	state.responseID = renderer.responseID
	state.model = renderer.model
	state.createdAt = renderer.createdAt
	if state.started {
		return nil
	}
	state.started = true
	base := map[string]any{"id": state.responseID, "object": "response", "created_at": state.createdAt, "status": "in_progress", "model": state.model, "output": []any{}}
	if err := renderer.writeResponsesEvent(MaheshvaraEventResponseCreated, map[string]any{"type": MaheshvaraEventResponseCreated, "response": base}); err != nil {
		return err
	}
	return renderer.writeResponsesEvent(MaheshvaraEventResponseInProgress, map[string]any{"type": MaheshvaraEventResponseInProgress, "response": base})
}

func (renderer *MaheshvaraStreamRenderer) ensureResponsesMessage(choiceIndex int) (*maheshvaraResponsesMessageState, error) {
	state := renderer.responses.messages[choiceIndex]
	if state != nil {
		return state, nil
	}
	state = &maheshvaraResponsesMessageState{id: newMaheshvaraResponseID("msg"), outputIndex: renderer.responses.nextOutput, extraParts: make(map[int]any)}
	state.parts[responsesPartText].index = -1
	state.parts[responsesPartRefusal].index = -1
	renderer.responses.nextOutput++
	renderer.responses.messages[choiceIndex] = state
	item := map[string]any{"id": state.id, "type": MaheshvaraOutputMessage, "status": "in_progress", "role": "assistant", "content": []any{}}
	if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemAdded, map[string]any{"type": MaheshvaraEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	return state, nil
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesText(choiceIndex int, text string) error {
	return renderer.writeResponsesPart(choiceIndex, responsesPartText, text)
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesText(choiceIndex int, text string) error {
	return renderer.finishResponsesPart(choiceIndex, responsesPartText, text)
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesRefusal(choiceIndex int, text string) error {
	return renderer.writeResponsesPart(choiceIndex, responsesPartRefusal, text)
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesRefusal(choiceIndex int, text string) error {
	return renderer.finishResponsesPart(choiceIndex, responsesPartRefusal, text)
}

// writeResponsesPart 输出一类内容槽的增量：首帧先发 ContentPartAdded，
// 之后逐帧 Delta。text 与 refusal 只是槽形状不同。
func (renderer *MaheshvaraStreamRenderer) writeResponsesPart(choiceIndex, partIndex int, text string) error {
	if text == "" {
		return nil
	}
	state, err := renderer.ensureResponsesMessage(choiceIndex)
	if err != nil {
		return err
	}
	meta := responsesPartMetas[partIndex]
	slot := state.slot(partIndex)
	if !slot.started {
		slot.started = true
		slot.index = nextResponsesContentIndex(state)
		part := map[string]any{"type": meta.partType, meta.eventKey: ""}
		// annotations 只属于 output_text：线制里 refusal part 无此键，
		// 多发会被严格客户端拒绝。
		if partIndex == responsesPartText {
			part["annotations"] = []any{}
		}
		if err := renderer.writeResponsesEvent(MaheshvaraEventContentPartAdded, map[string]any{"type": MaheshvaraEventContentPartAdded, "item_id": state.id, "output_index": state.outputIndex, "content_index": slot.index, "part": part}); err != nil {
			return err
		}
	}
	slot.text.WriteString(text)
	return renderer.writeResponsesEvent(meta.delta, map[string]any{"type": meta.delta, "item_id": state.id, "output_index": state.outputIndex, "content_index": slot.index, "delta": text})
}

// finishResponsesPart 关闭一类内容槽：终帧补齐零增量场景的全文并发 Done。
func (renderer *MaheshvaraStreamRenderer) finishResponsesPart(choiceIndex, partIndex int, text string) error {
	state := renderer.responses.messages[choiceIndex]
	if state == nil {
		return nil
	}
	meta := responsesPartMetas[partIndex]
	slot := state.slot(partIndex)
	if !slot.started || slot.done {
		return nil
	}
	if text != "" && slot.text.Len() == 0 {
		slot.text.WriteString(text)
	}
	slot.done = true
	return renderer.writeResponsesEvent(meta.done, map[string]any{"type": meta.done, "item_id": state.id, "output_index": state.outputIndex, "content_index": slot.index, meta.eventKey: slot.text.String()})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesReasoning(choiceIndex int, text string) error {
	if text == "" {
		return nil
	}
	state, err := renderer.ensureResponsesReasoning(choiceIndex)
	if err != nil {
		return err
	}
	state.text.WriteString(text)
	return renderer.writeResponsesEvent(MaheshvaraEventReasoningSummaryDelta, map[string]any{"type": MaheshvaraEventReasoningSummaryDelta, "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "delta": text})
}

// ensureResponsesReasoning 保证 reasoning item 已对下游宣告（签名/密文可能先
// 于文本到达，也需要挂在同一个 item 上）。
func (renderer *MaheshvaraStreamRenderer) ensureResponsesReasoning(choiceIndex int) (*maheshvaraResponsesReasoningState, error) {
	state := renderer.responses.reasoning[choiceIndex]
	if state != nil {
		return state, nil
	}
	state = &maheshvaraResponsesReasoningState{id: newMaheshvaraResponseID("rs"), outputIndex: renderer.responses.nextOutput}
	renderer.responses.nextOutput++
	renderer.responses.reasoning[choiceIndex] = state
	item := map[string]any{"id": state.id, "type": MaheshvaraOutputReasoning, "status": "in_progress", "summary": []any{}}
	if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemAdded, map[string]any{"type": MaheshvaraEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	if err := renderer.writeResponsesEvent("response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}); err != nil {
		return nil, err
	}
	return state, nil
}

// writeResponsesReasoningSignature 输出签名增量（跨协议推理闭环：下游把签名
// 原样回传，加密思考得以在下一轮续用）。
func (renderer *MaheshvaraStreamRenderer) writeResponsesReasoningSignature(choiceIndex int, signature string) error {
	if signature == "" {
		return nil
	}
	state, err := renderer.ensureResponsesReasoning(choiceIndex)
	if err != nil {
		return err
	}
	state.signature.WriteString(signature)
	return renderer.writeResponsesEvent(MaheshvaraEventReasoningSignatureDelta, map[string]any{"type": MaheshvaraEventReasoningSignatureDelta, "item_id": state.id, "output_index": state.outputIndex, "delta": signature})
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesReasoning(choiceIndex int, text string) error {
	state := renderer.responses.reasoning[choiceIndex]
	if state == nil || state.done {
		return nil
	}
	if text != "" && state.text.Len() == 0 {
		state.text.WriteString(text)
	}
	state.done = true
	return renderer.writeResponsesEvent(MaheshvaraEventReasoningSummaryDone, map[string]any{"type": MaheshvaraEventReasoningSummaryDone, "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "text": state.text.String()})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesContentPart(event *MaheshvaraStreamEvent) error {
	if event == nil || event.ContentPart == nil {
		return nil
	}
	part, ok := maheshvaraPartToResponsesOutputContent(*event.ContentPart)
	if !ok {
		if raw, rawOK := event.ContentPart.Raw.(map[string]any); rawOK && len(raw) > 0 {
			partMap := raw
			return renderer.addResponsesExtraPart(event.ChoiceIndex, partMap)
		}
		return nil
	}
	encoded, err := json.Marshal(part)
	if err != nil {
		return err
	}
	var partMap map[string]any
	if err := json.Unmarshal(encoded, &partMap); err != nil {
		return err
	}
	return renderer.addResponsesExtraPart(event.ChoiceIndex, partMap)
}

func (renderer *MaheshvaraStreamRenderer) addResponsesExtraPart(choiceIndex int, part any) error {
	state, err := renderer.ensureResponsesMessage(choiceIndex)
	if err != nil {
		return err
	}
	contentIndex := nextResponsesContentIndex(state)
	state.extraParts[contentIndex] = part
	payload := map[string]any{"type": MaheshvaraEventContentPartAdded, "item_id": state.id, "output_index": state.outputIndex, "content_index": contentIndex, "part": part}
	if err := renderer.writeResponsesEvent(MaheshvaraEventContentPartAdded, payload); err != nil {
		return err
	}
	payload["type"] = MaheshvaraEventContentPartDone
	return renderer.writeResponsesEvent(MaheshvaraEventContentPartDone, payload)
}

func nextResponsesContentIndex(state *maheshvaraResponsesMessageState) int {
	for index := 0; ; index++ {
		if state.parts[responsesPartText].started && state.parts[responsesPartText].index == index {
			continue
		}
		if state.parts[responsesPartRefusal].started && state.parts[responsesPartRefusal].index == index {
			continue
		}
		if _, exists := state.extraParts[index]; exists {
			continue
		}
		return index
	}
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesTool(event *MaheshvaraStreamEvent) error {
	// 按调用序号定键：id 可能在首个增量之后才到达，按 id 定键会把同一逻辑
	// 调用分裂成两个状态（参数被拆到两个 function_call item）。id 只作属性。
	key := fmt.Sprintf("choice_%d_tool_%d", event.ChoiceIndex, event.ToolCallIndex)
	state := renderer.responses.tools[key]
	if state == nil {
		state = &maheshvaraResponsesToolState{id: newMaheshvaraResponseID("fc"), callID: event.ToolCallID, name: event.ToolName, outputIndex: renderer.responses.nextOutput}
		renderer.responses.nextOutput++
		renderer.responses.tools[key] = state
		renderer.responses.toolOrder = append(renderer.responses.toolOrder, key)
	}
	state.callID = firstNonEmptyString(event.ToolCallID, state.callID, state.id)
	if state.callID == "" {
		// function_call 事件的 call_id 缺失时合成稳定 ID，避免下游
		// 回传 function_call_output 时对不上调用。
		state.callID = ensureToolCallID("", event.ToolCallIndex, 0)
	}
	state.name = firstNonEmptyString(event.ToolName, state.name)
	if !state.added {
		state.added = true
		item := map[string]any{"id": state.id, "type": MaheshvaraOutputFunctionCall, "status": "in_progress", "call_id": state.callID, "name": state.name, "arguments": ""}
		if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemAdded, map[string]any{"type": MaheshvaraEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
			return err
		}
	}
	argumentDelta := event.ToolArgumentsDelta
	if event.ToolArgumentsDone != "" {
		argumentDelta = applyToolArgumentDelta(&state.arguments, *event)
	}
	if argumentDelta != "" {
		state.arguments.WriteString(argumentDelta)
		if err := renderer.writeResponsesEvent(MaheshvaraEventFunctionCallArgumentsDelta, map[string]any{"type": MaheshvaraEventFunctionCallArgumentsDelta, "item_id": state.id, "output_index": state.outputIndex, "delta": argumentDelta}); err != nil {
			return err
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesOutputItem(event *MaheshvaraStreamEvent) error {
	item := event.OutputItem
	if item == nil {
		return nil
	}
	switch item.Type {
	case MaheshvaraOutputFunctionCall:
		added := &MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallAdded, ToolCallIndex: event.OutputIndex, ToolCallID: item.CallID, ToolName: item.Name}
		if err := renderer.writeResponsesTool(added); err != nil {
			return err
		}
		if len(item.Arguments) > 0 {
			added.Type = MaheshvaraEventFunctionCallArgumentsDone
			added.ToolArgumentsDone = string(item.Arguments)
			return renderer.writeResponsesTool(added)
		}
	case MaheshvaraOutputReasoning:
		state, err := renderer.ensureResponsesReasoning(event.ChoiceIndex)
		if err != nil {
			return err
		}
		if encrypted := maheshvaraReasoningEncryptedContent(*item); encrypted != "" {
			state.encrypted = encrypted
		}
		return renderer.writeResponsesReasoning(event.ChoiceIndex, maheshvaraReasoningText(*item))
	default:
		for index := range item.Content {
			part := item.Content[index]
			switch part.Type {
			case MaheshvaraContentText:
				if err := renderer.writeResponsesText(event.ChoiceIndex, part.Text); err != nil {
					return err
				}
			case MaheshvaraContentReasoning:
				if err := renderer.writeResponsesReasoning(event.ChoiceIndex, firstNonEmptyString(part.ReasoningText, part.Text)); err != nil {
					return err
				}
			case MaheshvaraContentRefusal:
				if err := renderer.writeResponsesRefusal(event.ChoiceIndex, part.Text); err != nil {
					return err
				}
			default:
				if err := renderer.writeResponsesContentPart(&MaheshvaraStreamEvent{ChoiceIndex: event.ChoiceIndex, ContentPart: &part}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type maheshvaraResponsesRenderedOutput struct {
	index int
	item  map[string]any
}

func (renderer *MaheshvaraStreamRenderer) completeResponses() error {
	state := renderer.responses
	if state.completed {
		return nil
	}
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	var outputs []maheshvaraResponsesRenderedOutput
	// 各类 item 的收尾事件先收集、统一按 outputIndex 排序后发出。
	// 按类别顺序（message→reasoning→tool）发出时，多 choice 场景下
	// output_item.done 事件次序与 output_index 不一致，逐事件消费的
	// 客户端会看到乱序的输出项。
	type deferredDone struct {
		index int
		run   func() error
	}
	pending := make([]deferredDone, 0, len(state.messages)+len(state.reasoning)+len(state.tools))

	for choiceIndex, message := range state.messages {
		if message == nil || message.done {
			continue
		}
		pending = append(pending, deferredDone{index: message.outputIndex, run: func() error {
			item, err := renderer.finalizeResponsesMessage(choiceIndex, message)
			if err != nil {
				return err
			}
			outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: message.outputIndex, item: item})
			return nil
		}})
	}
	for choiceIndex, reasoning := range state.reasoning {
		if reasoning == nil {
			continue
		}
		pending = append(pending, deferredDone{index: reasoning.outputIndex, run: func() error {
			item, err := renderer.finalizeResponsesReasoning(choiceIndex, reasoning)
			if err != nil {
				return err
			}
			outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: reasoning.outputIndex, item: item})
			return nil
		}})
	}
	for _, key := range state.toolOrder {
		tool := state.tools[key]
		if tool == nil || tool.done {
			continue
		}
		pending = append(pending, deferredDone{index: tool.outputIndex, run: func() error {
			item, err := renderer.finalizeResponsesTool(tool)
			if err != nil {
				return err
			}
			outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: tool.outputIndex, item: item})
			return nil
		}})
	}
	sort.Slice(pending, func(left, right int) bool { return pending[left].index < pending[right].index })
	for _, done := range pending {
		if err := done.run(); err != nil {
			return err
		}
	}
	sort.Slice(outputs, func(left, right int) bool { return outputs[left].index < outputs[right].index })
	outputItems := make([]any, 0, len(outputs))
	for _, output := range outputs {
		outputItems = append(outputItems, output.item)
	}
	usage := renderer.usage
	if usage == nil {
		usage = &MaheshvaraUsage{}
	}
	completed := map[string]any{"id": renderer.responseID, "object": "response", "created_at": renderer.createdAt, "status": "completed", "model": renderer.model, "output": outputItems, "usage": responsesUsageFromMaheshvara(usage)}
	state.completed = true
	return renderer.writeResponsesEvent(MaheshvaraEventResponseCompleted, map[string]any{"type": MaheshvaraEventResponseCompleted, "response": completed})
}

// finalizeResponsesMessage 补发未收尾的文本/refusal part done 帧并发出
// message item 的 output_item.done，返回终态 item。
func (renderer *MaheshvaraStreamRenderer) finalizeResponsesMessage(choiceIndex int, message *maheshvaraResponsesMessageState) (map[string]any, error) {
	for _, partIndex := range []int{responsesPartText, responsesPartRefusal} {
		slot := message.slot(partIndex)
		if slot.started && !slot.done {
			if err := renderer.finishResponsesPart(choiceIndex, partIndex, ""); err != nil {
				return nil, err
			}
		}
	}
	content := make([]any, responsesMessageContentCount(message))
	for _, partIndex := range []int{responsesPartText, responsesPartRefusal} {
		slot := message.slot(partIndex)
		if !slot.started {
			continue
		}
		meta := responsesPartMetas[partIndex]
		part := map[string]any{"type": meta.partType, meta.eventKey: slot.text.String()}
		if partIndex == responsesPartText {
			part["annotations"] = []any{}
		}
		content[slot.index] = part
		if err := renderer.writeResponsesEvent(MaheshvaraEventContentPartDone, map[string]any{"type": MaheshvaraEventContentPartDone, "item_id": message.id, "output_index": message.outputIndex, "content_index": slot.index, "part": part}); err != nil {
			return nil, err
		}
	}
	for index, part := range message.extraParts {
		content[index] = part
	}
	content = compactResponsesContent(content)
	item := map[string]any{"id": message.id, "type": MaheshvaraOutputMessage, "status": "completed", "role": "assistant", "content": content}
	if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemDone, map[string]any{"type": MaheshvaraEventOutputItemDone, "output_index": message.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	message.done = true
	return item, nil
}

// finalizeResponsesReasoning 收尾 reasoning item：finish 幂等（流中已正常
// 收尾的直接跳过）、summary part done 与 output_item.done；跨协议时把加密
// 思考随终态 item 回写，下游续轮原样带回。
func (renderer *MaheshvaraStreamRenderer) finalizeResponsesReasoning(choiceIndex int, reasoning *maheshvaraResponsesReasoningState) (map[string]any, error) {
	if err := renderer.finishResponsesReasoning(choiceIndex, ""); err != nil {
		return nil, err
	}
	part := map[string]any{"type": "summary_text", "text": reasoning.text.String()}
	if err := renderer.writeResponsesEvent("response.reasoning_summary_part.done", map[string]any{"type": "response.reasoning_summary_part.done", "item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0, "part": part}); err != nil {
		return nil, err
	}
	item := map[string]any{"id": reasoning.id, "type": MaheshvaraOutputReasoning, "status": "completed", "summary": []any{part}}
	if reasoning.encrypted != "" {
		item["encrypted_content"] = reasoning.encrypted
	}
	if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemDone, map[string]any{"type": MaheshvaraEventOutputItemDone, "output_index": reasoning.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	return item, nil
}

// finalizeResponsesTool 收尾 function_call item：未宣告的先补 added 帧，再发
// 参数 done（空参数归一为 "{}"）与 output_item.done。
func (renderer *MaheshvaraStreamRenderer) finalizeResponsesTool(tool *maheshvaraResponsesToolState) (map[string]any, error) {
	if !tool.added {
		if err := renderer.writeResponsesTool(&MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallAdded, ToolCallID: tool.callID, ToolName: tool.name, ToolCallIndex: tool.outputIndex}); err != nil {
			return nil, err
		}
	}
	arguments := tool.arguments.String()
	if arguments == "" {
		arguments = "{}"
	}
	if err := renderer.writeResponsesEvent(MaheshvaraEventFunctionCallArgumentsDone, map[string]any{"type": MaheshvaraEventFunctionCallArgumentsDone, "item_id": tool.id, "output_index": tool.outputIndex, "arguments": arguments}); err != nil {
		return nil, err
	}
	item := map[string]any{"id": tool.id, "type": MaheshvaraOutputFunctionCall, "status": "completed", "call_id": tool.callID, "name": tool.name, "arguments": arguments}
	if err := renderer.writeResponsesEvent(MaheshvaraEventOutputItemDone, map[string]any{"type": MaheshvaraEventOutputItemDone, "output_index": tool.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	tool.done = true
	return item, nil
}

func responsesMessageContentCount(state *maheshvaraResponsesMessageState) int {
	maxIndex := -1
	for _, partIndex := range []int{responsesPartText, responsesPartRefusal} {
		slot := state.slot(partIndex)
		if slot.started && slot.index > maxIndex {
			maxIndex = slot.index
		}
	}
	for index := range state.extraParts {
		if index > maxIndex {
			maxIndex = index
		}
	}
	return maxIndex + 1
}

func compactResponsesContent(content []any) []any {
	result := make([]any, 0, len(content))
	for _, part := range content {
		if part != nil {
			result = append(result, part)
		}
	}
	return result
}

func (renderer *MaheshvaraStreamRenderer) finishResponses() error {
	if renderer.responses.completed {
		return nil
	}
	return renderer.completeResponses()
}

// abortResponses 按官方规范发两帧:平铺 error 事件(无 error 包裹)与
// response.failed(response.status=failed + response.error={code,message})。
func (renderer *MaheshvaraStreamRenderer) abortResponses(mErr *MaheshvaraError) error {
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	_, code := mErr.Class.openAITypeCode()
	if mErr.Code != "" {
		code = mErr.Code
	}
	errorPayload := map[string]any{"type": "error", "code": nullableString(code), "message": mErr.Message, "param": nil}
	if err := renderer.writeResponsesEvent("error", errorPayload); err != nil {
		return err
	}
	failed := map[string]any{"id": renderer.responseID, "object": "response", "created_at": renderer.createdAt, "status": "failed", "model": renderer.model, "output": []any{}, "error": map[string]any{"code": nullableString(code), "message": mErr.Message}}
	renderer.responses.completed = true
	return renderer.writeResponsesEvent(MaheshvaraEventResponseFailed, map[string]any{"type": MaheshvaraEventResponseFailed, "response": failed})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesEvent(eventType string, payload map[string]any) error {
	renderer.responses.sequence++
	payload["sequence_number"] = renderer.responses.sequence
	if payload["type"] == nil {
		payload["type"] = eventType
	}
	return renderer.writeSSEEvent(eventType, payload)
}
