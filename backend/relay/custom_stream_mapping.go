package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

type CustomProtocolStreamDecoder struct {
	config            CustomProtocolConfig
	mode              string
	modeText          string
	modeReasoning     string
	modeArgs          string
	doneValues        map[string]struct{}
	doneJSON          []any
	events            map[string]struct{}
	eventKeys         []string
	finishWhen        *CustomProtocolMatch
	statusWhen        *CustomProtocolMatch
	frames            []CustomProtocolStreamFrame
	previousText      map[string]string
	previousReasoning map[string]string
	previousArguments map[string]string
	toolAdded         map[string]bool
	toolSlot          map[string]int
	nextToolSlot      int
	frameTools        map[float64]CustomProtocolStreamToolIdentity
	terminal          bool
	sawOutput         bool
	sawFinish         bool
}

// CustomProtocolStreamToolIdentity 记录身份帧（content_block_start /
// output_item.added）声明的工具身份，供仅携带 index 的参数帧关联。
type CustomProtocolStreamToolIdentity struct {
	ID   string
	Name string
}

func NewCustomProtocolStreamDecoder(config CustomProtocolConfig) (*CustomProtocolStreamDecoder, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	decoder := &CustomProtocolStreamDecoder{
		config:            config,
		mode:              "delta",
		doneValues:        map[string]struct{}{"[DONE]": {}},
		events:            make(map[string]struct{}),
		eventKeys:         []string{"type", "event"},
		previousText:      make(map[string]string),
		previousReasoning: make(map[string]string),
		previousArguments: make(map[string]string),
		toolAdded:         make(map[string]bool),
		toolSlot:          make(map[string]int),
		frameTools:        make(map[float64]CustomProtocolStreamToolIdentity),
	}
	if stream := config.Response.Stream; stream != nil {
		if mode := strings.ToLower(strings.TrimSpace(stream.Mode)); mode != "" {
			decoder.mode = mode
		}
		decoder.modeText = decoder.mode
		decoder.modeReasoning = decoder.mode
		decoder.modeArgs = decoder.mode
		if modes := stream.Modes; modes != nil {
			if family := strings.ToLower(strings.TrimSpace(modes.Text)); family != "" {
				decoder.modeText = family
			}
			if family := strings.ToLower(strings.TrimSpace(modes.Reasoning)); family != "" {
				decoder.modeReasoning = family
			}
			if family := strings.ToLower(strings.TrimSpace(modes.Arguments)); family != "" {
				decoder.modeArgs = family
			}
		}
		if stream.DoneValuesReplace {
			decoder.doneValues = make(map[string]struct{})
		}
		for _, value := range stream.DoneValues {
			value = strings.TrimSpace(value)
			if value != "" {
				decoder.doneValues[value] = struct{}{}
			}
		}
		for _, done := range stream.Done {
			if strings.TrimSpace(done.Raw) != "" {
				decoder.doneValues[strings.TrimSpace(done.Raw)] = struct{}{}
			}
			if len(done.JSON) > 0 {
				if parsed, ok := customMatchValue(done.JSON); ok {
					decoder.doneJSON = append(decoder.doneJSON, parsed)
				}
			}
		}
		for _, eventName := range stream.Events {
			eventName = strings.TrimSpace(eventName)
			if eventName != "" {
				decoder.events[eventName] = struct{}{}
			}
		}
		if len(stream.EventKeys) > 0 {
			decoder.eventKeys = stream.EventKeys
		}
		decoder.finishWhen = stream.FinishWhen
		decoder.statusWhen = stream.StatusWhen
		for _, frame := range stream.Frames {
			frame.Event = strings.TrimSpace(frame.Event)
			decoder.frames = append(decoder.frames, frame)
		}
	}
	return decoder, nil
}

func (decoder *CustomProtocolStreamDecoder) TerminalReceived() bool {
	return decoder != nil && decoder.terminal
}

func (decoder *CustomProtocolStreamDecoder) SawOutput() bool {
	return decoder != nil && decoder.sawOutput
}

// SawFinishReason 报告流中是否出现过非空 finish reason。空补全（零输出但
// finish_reason 有值，如内容过滤 stop）据此与「[DONE] 兜底空流」区分开。
func (decoder *CustomProtocolStreamDecoder) SawFinishReason() bool {
	return decoder != nil && decoder.sawFinish
}

// Decode 解析一帧上游事件。第二个返回值仅在该帧命中终止值（doneValues/done）
// 时为 true（数据此后不会再有）；终止判定（finish reason / status）只置终态，
// 不提前结束——调用方继续排水以接收 usage 尾帧等滞后事件。
func (decoder *CustomProtocolStreamDecoder) Decode(wireEvent SSEEvent) ([]MaheshvaraStreamEvent, bool, error) {
	if decoder == nil {
		return nil, false, fmt.Errorf("nil custom protocol stream decoder")
	}
	data := strings.TrimSpace(wireEvent.Data)
	if data == "" {
		return nil, false, nil
	}
	if _, done := decoder.doneValues[data]; done {
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: MaheshvaraEventResponseCompleted}}, true, nil
	}
	if len(decoder.doneJSON) > 0 && decoder.matchDoneJSON(data) {
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: MaheshvaraEventResponseCompleted}}, true, nil
	}
	config := decoder.config
	terminalFrame := false
	var frameTool *CustomProtocolStreamTool
	var frameRoot any
	if len(decoder.frames) > 0 {
		root, rootOK := customMatchValue(json.RawMessage(data))
		eventName := ""
		if rootOK {
			eventName = decoder.payloadEventName(strings.TrimSpace(wireEvent.Event), root)
		} else if trimmed := strings.TrimSpace(wireEvent.Event); trimmed != "" {
			eventName = trimmed
		}
		frame := decoder.matchFrame(eventName, root, rootOK)
		if frame == nil {
			// 异构流中未声明的帧型不属于本协议语义，跳过；需要兜底映射时
			// 用 stream.response 声明默认映射。
			return nil, false, nil
		}
		if frame.Response != nil || strings.TrimSpace(frame.PayloadPath) != "" {
			config = customProtocolFrameConfig(decoder.config, *frame)
		}
		terminalFrame = frame.Terminal
		frameTool = frame.Tool
		frameRoot = root
	} else if len(decoder.events) > 0 {
		eventName := decoder.wireEventName(wireEvent, data)
		if _, allowed := decoder.events[eventName]; !allowed {
			return nil, false, nil
		}
	}
	response, err := customProtocolStreamEventToMaheshvaraValidated([]byte(data), config)
	if err != nil {
		return nil, false, err
	}
	if frameTool != nil {
		if item, ok := decoder.frameToolItem(frameTool, frameRoot); ok {
			response.Output = append(response.Output, item)
		}
	}
	events := decoder.contentEvents(response, decoder.frameArgsMode(frameTool))
	// 终止判定：finishWhen/statusWhen 配置时按 Match 语义（载荷根为
	// payloadPath 解包后的对象）；缺省沿用 legacy——finishReasonPath 字符串化
	// 非空、status == "completed"。
	root := decoder.matchRoot(data, config)
	finishHit := response.StopReason != ""
	if decoder.finishWhen != nil {
		finishHit = root != nil && customMatchEval(root, *decoder.finishWhen)
	}
	statusHit := response.Status == "completed"
	if decoder.statusWhen != nil {
		statusHit = root != nil && customMatchEval(root, *decoder.statusWhen)
	}
	if finishHit || statusHit {
		if finishHit {
			decoder.sawFinish = true
		}
		events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventResponseCompleted, ResponseID: response.ID, Model: response.Model, FinishReason: response.StopReason, Response: response})
	}
	if terminalFrame {
		decoder.terminal = true
		if !finishHit && !statusHit {
			// 帧型终止：映射本身未产生终态事件时补一个空完成事件，
			// 保证渲染侧仍能拿到 finish。
			events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventResponseCompleted, ResponseID: response.ID, Model: response.Model})
		}
	}
	for _, event := range events {
		if maheshvaraStreamEventHasOutput(event) {
			decoder.sawOutput = true
		}
		if event.Type == MaheshvaraEventResponseCompleted || event.Type == MaheshvaraEventResponseFailed {
			decoder.terminal = true
		}
	}
	return events, false, nil
}

// matchDoneJSON 判定整帧载荷是否类型化等于任一配置的 done JSON 值。
func (decoder *CustomProtocolStreamDecoder) matchDoneJSON(data string) bool {
	parsed, ok := customMatchValue(json.RawMessage(data))
	if !ok {
		return false
	}
	for _, expected := range decoder.doneJSON {
		if customJSONValuesEqual(parsed, expected) {
			return true
		}
	}
	return false
}

// matchRoot 解析终止判定的求值根：payloadPath 解包后的载荷（与响应映射同根）。
// 仅在配置了 finishWhen/statusWhen 时解析；解析失败返回 nil（Match 不成立）。
func (decoder *CustomProtocolStreamDecoder) matchRoot(data string, config CustomProtocolConfig) any {
	if decoder.finishWhen == nil && decoder.statusWhen == nil {
		return nil
	}
	raw, ok := customMatchValue(json.RawMessage(data))
	if !ok {
		return nil
	}
	if stream := config.Response.Stream; stream != nil {
		if payloadPath := strings.TrimSpace(stream.PayloadPath); payloadPath != "" {
			if payload, found := customLookupPath(raw, payloadPath); found {
				return payload
			}
		}
	}
	return raw
}

// wireEventName 取帧的事件名：优先 SSE event 字段，缺省时按 eventKeys（默认
// type/event）回落 JSON 载荷字段（Responses 型协议把类型写在数据里）。
func (decoder *CustomProtocolStreamDecoder) wireEventName(wireEvent SSEEvent, data string) string {
	if eventName := strings.TrimSpace(wireEvent.Event); eventName != "" {
		return eventName
	}
	if raw, err := decodeSSEEventJSON(data); err == nil {
		return decoder.payloadEventName("", raw)
	}
	return ""
}

func (decoder *CustomProtocolStreamDecoder) matchFrame(eventName string, root any, rootOK bool) *CustomProtocolStreamFrame {
	for index := range decoder.frames {
		frame := decoder.frames[index]
		if frame.Event != "" {
			if eventName == "" || frame.Event != eventName {
				continue
			}
		}
		if frame.Match != nil {
			if !rootOK || !customMatchEval(root, *frame.Match) {
				continue
			}
		}
		matched := frame
		return &matched
	}
	return nil
}

// payloadEventName 在已解析的帧载荷上按 eventKeys 取事件名；SSE event 字段
// 非空时优先。
func (decoder *CustomProtocolStreamDecoder) payloadEventName(wireEventName string, root any) string {
	if wireEventName != "" {
		return wireEventName
	}
	object, _ := root.(map[string]any)
	if object == nil {
		return ""
	}
	for _, key := range decoder.eventKeys {
		if name := stringValue(object[key]); name != "" {
			return name
		}
	}
	return ""
}

// frameToolItem 从帧 JSON 组装工具调用增量：身份帧（id/name 可得）注册
// index→身份；参数帧经 index 关联或直接携带 id。仅身份无参数时 Arguments
// 留空（不产生参数增量，避免 "{}" 混入拼装流）。
func (decoder *CustomProtocolStreamDecoder) frameToolItem(tool *CustomProtocolStreamTool, root any) (MaheshvaraOutputItem, bool) {
	if tool == nil || root == nil {
		return MaheshvaraOutputItem{}, false
	}
	id := customStringAt(root, tool.IDPath)
	name := customStringAt(root, tool.NamePath)
	index, hasIndex := 0.0, false
	if strings.TrimSpace(tool.IndexPath) != "" {
		if number, ok := numberValue(customValueAt(root, tool.IndexPath)); ok {
			index, hasIndex = number, true
		}
	}
	if hasIndex {
		if identity, found := decoder.frameTools[index]; found {
			if id == "" {
				id = identity.ID
			}
			if name == "" {
				name = identity.Name
			}
		} else if id != "" || name != "" {
			decoder.frameTools[index] = CustomProtocolStreamToolIdentity{ID: id, Name: name}
		}
	}
	if id == "" && name == "" {
		return MaheshvaraOutputItem{}, false // 无身份可关联（身份帧未到）
	}
	item := MaheshvaraOutputItem{
		Type: MaheshvaraOutputFunctionCall, Status: "completed",
		CallID: id, Name: name,
	}
	if strings.TrimSpace(tool.ArgumentsPath) != "" {
		if arguments := customValueAt(root, tool.ArgumentsPath); arguments != nil {
			if text, ok := arguments.(string); ok {
				// 参数片段按定义可能是不完整 JSON（input_json_delta 分片），
				// 原样透传供拼接，不做「无效 JSON 加引号」保护。
				item.Arguments = json.RawMessage(text)
			} else {
				item.Arguments, _ = json.Marshal(arguments)
			}
		}
	}
	return item, true
}

// frameArgsMode 计算本帧工具参数的差分模式：帧级 argumentsMode 覆盖族级。
func (decoder *CustomProtocolStreamDecoder) frameArgsMode(tool *CustomProtocolStreamTool) string {
	if tool != nil {
		if mode := strings.ToLower(strings.TrimSpace(tool.ArgumentsMode)); mode == "delta" || mode == "cumulative" {
			return mode
		}
	}
	return decoder.modeArgs
}

// customProtocolFrameConfig 把命中的帧规则叠加到协议配置副本上：帧映射覆盖
// 默认映射，payloadPath 缺省继承流级配置。帧内 response 不得再携带流配置
// （校验已保证），置 nil 防止嵌套语义被运行时重复应用。
func customProtocolFrameConfig(config CustomProtocolConfig, frame CustomProtocolStreamFrame) CustomProtocolConfig {
	stream := CustomProtocolStreamMapping{}
	if config.Response.Stream != nil {
		stream = *config.Response.Stream
	}
	if payloadPath := strings.TrimSpace(frame.PayloadPath); payloadPath != "" {
		stream.PayloadPath = payloadPath
	}
	if frame.Response != nil {
		nested := *frame.Response
		nested.Stream = nil
		stream.Response = &nested
	}
	config.Response.Stream = &stream
	return config
}

// contentEvents 产生一帧映射出的内容/工具/用量事件；终止事件由 Decode 统一
// 判定（需要访问原始载荷以求值 Match）。
func (decoder *CustomProtocolStreamDecoder) contentEvents(response *MaheshvaraResponse, argsMode string) []MaheshvaraStreamEvent {
	if response == nil {
		return nil
	}
	var events []MaheshvaraStreamEvent
	if response.Error != nil {
		events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventResponseFailed, ResponseID: response.ID, Model: response.Model, Error: response.Error})
		return events
	}
	for outputIndex, item := range response.Output {
		switch item.Type {
		case MaheshvaraOutputFunctionCall:
			key := firstNonEmptyString(item.CallID, item.Name, fmt.Sprintf("tool_%d", outputIndex))
			// 下游渲染器按 ToolCallIndex 组装工具状态;本帧 Output 数组下标
			// 是临时位置,跨帧的多个工具会全部撞在 0——改用流级稳定槽位
			//(身份键首次出现时分配,此后不变)。
			slot, seen := decoder.toolSlot[key]
			if !seen {
				slot = decoder.nextToolSlot
				decoder.nextToolSlot++
				decoder.toolSlot[key] = slot
			}
			if !decoder.toolAdded[key] {
				decoder.toolAdded[key] = true
				events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: slot, ToolCallIndex: slot, ToolCallID: item.CallID, ToolName: item.Name})
			}
			arguments := string(item.Arguments)
			if arguments != "" {
				delta := decoder.streamDelta(decoder.previousArguments, key, arguments, argsMode)
				if delta != "" {
					events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventFunctionCallArgumentsDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: slot, ToolCallIndex: slot, ToolCallID: item.CallID, ToolName: item.Name, ToolArgumentsDelta: delta})
				}
			}
		case MaheshvaraOutputReasoning:
			text := maheshvaraReasoningText(item)
			delta := decoder.streamDelta(decoder.previousReasoning, fmt.Sprintf("reasoning_%d", outputIndex), text, decoder.modeReasoning)
			if delta != "" {
				events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ItemID: item.ID, ReasoningDelta: delta})
			}
		default:
			for contentIndex, part := range item.Content {
				key := fmt.Sprintf("%d:%d", outputIndex, contentIndex)
				switch part.Type {
				case MaheshvaraContentText:
					delta := decoder.streamDelta(decoder.previousText, key, part.Text, decoder.modeText)
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventTextDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, Delta: delta})
					}
				case MaheshvaraContentReasoning:
					delta := decoder.streamDelta(decoder.previousReasoning, key, firstNonEmptyString(part.ReasoningText, part.Text), decoder.modeReasoning)
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, ReasoningDelta: delta})
					}
				case MaheshvaraContentRefusal:
					delta := decoder.streamDelta(decoder.previousText, "refusal:"+key, part.Text, decoder.modeText)
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventRefusalDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, RefusalDelta: delta})
					}
				default:
					partCopy := part
					events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventContentPartAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, ContentPart: &partCopy})
				}
			}
		}
	}
	if response.Usage != nil {
		events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventUsageDelta, ResponseID: response.ID, Model: response.Model, Usage: response.Usage})
	}
	return events
}

func (decoder *CustomProtocolStreamDecoder) streamDelta(previous map[string]string, key, current, mode string) string {
	if mode != "cumulative" {
		return current
	}
	before := previous[key]
	previous[key] = current
	switch {
	case current == before:
		return ""
	case strings.HasPrefix(current, before):
		return strings.TrimPrefix(current, before)
	default:
		return current
	}
}
