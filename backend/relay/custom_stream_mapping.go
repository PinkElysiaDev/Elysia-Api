package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type CustomProtocolStreamDecoder struct {
	config            CustomProtocolConfig
	aliases           *CustomProtocolAliases
	resolved          customResolvedMapping
	frameResolved     []customResolvedMapping
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
// 流增量差分的身份键格式:文本/推理/工具参数各自独立累计,键形状是拼装
// 语义的一部分(跨帧身份关联依赖其稳定性)。
const (
	customStreamKeyTextFmt      = "%d:%d"
	customStreamKeyRefusalFmt   = "refusal:%d:%d"
	customStreamKeyReasoningFmt = "reasoning_%d"
	customStreamKeyToolFmt      = "tool_%d"
	customDoneSentinel          = "[DONE]"
)

type CustomProtocolStreamToolIdentity struct {
	ID   string
	Name string
}

// NewCustomProtocolStreamDecoder 校验并构造流解码器(外部传入的协议配置:
// 预览/测试/助手等非注册路径)。已注册协议走 NewRegisteredCustomProtocolStreamDecoder。
func NewCustomProtocolStreamDecoder(config CustomProtocolConfig) (*CustomProtocolStreamDecoder, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	return newCustomProtocolStreamDecoder(config)
}

// NewRegisteredCustomProtocolStreamDecoder 为注册表内协议构造流解码器:
// 入库时已整体校验(含双模板渲染),热路径不再重复。
func NewRegisteredCustomProtocolStreamDecoder(config CustomProtocolConfig) (*CustomProtocolStreamDecoder, error) {
	return newCustomProtocolStreamDecoder(config)
}

func newCustomProtocolStreamDecoder(config CustomProtocolConfig) (*CustomProtocolStreamDecoder, error) {
	decoder := &CustomProtocolStreamDecoder{
		aliases:           config.Aliases,
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
	// 运行时映射在构造时对默认路径与每个帧各预编译一份,事件循环零编译。
	resolved, err := resolveCustomMapping(config, true)
	if err != nil {
		return nil, err
	}
	decoder.resolved = resolved
	decoder.frameResolved = make([]customResolvedMapping, len(decoder.frames))
	for index, frame := range decoder.frames {
		frameConfig := config
		if frame.Response != nil || strings.TrimSpace(frame.PayloadPath) != "" {
			frameConfig = customProtocolFrameConfig(config, frame)
		}
		if decoder.frameResolved[index], err = resolveCustomMapping(frameConfig, true); err != nil {
			return nil, err
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
	// 帧载荷只解析一次:帧匹配、响应映射与终止判定共用同一 root。
	root, parseErr := decodeJSONUseNumber([]byte(data))
	rootOK := parseErr == nil
	if _, done := decoder.doneValues[data]; done {
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: MaheshvaraEventResponseCompleted}}, true, nil
	}
	if len(decoder.doneJSON) > 0 && rootOK && matchJSONDoneValue(root, decoder.doneJSON) {
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: MaheshvaraEventResponseCompleted}}, true, nil
	}
	resolved := decoder.resolved
	terminalFrame := false
	var frameTool *CustomProtocolStreamTool
	if len(decoder.frames) > 0 {
		eventName := decoder.effectiveEventName(wireEvent.Event, root, rootOK)
		frameIndex := decoder.matchFrameIndex(eventName, root, rootOK)
		if frameIndex < 0 {
			// 异构流中未声明的帧型不属于本协议语义，跳过；需要兜底映射时
			// 用 stream.response 声明默认映射。
			return nil, false, nil
		}
		resolved = decoder.frameResolved[frameIndex]
		terminalFrame = decoder.frames[frameIndex].Terminal
		frameTool = decoder.frames[frameIndex].Tool
	} else if len(decoder.events) > 0 {
		eventName := decoder.effectiveEventName(wireEvent.Event, root, rootOK)
		if _, allowed := decoder.events[eventName]; !allowed {
			return nil, false, nil
		}
	}
	if !rootOK {
		return nil, false, fmt.Errorf("failed to parse custom protocol stream event: %w", parseErr)
	}
	response, err := customProtocolResponseFromRoot(root, resolved, decoder.aliases, decoder.config.ID, true)
	if err != nil {
		return nil, false, err
	}
	if frameTool != nil {
		response.Output = append(response.Output, decoder.frameToolItems(frameTool, root)...)
	}
	events := decoder.buildContentEvents(response, decoder.frameArgsMode(frameTool))
	// 终止判定：finishWhen/statusWhen 配置时按 Match 语义（对帧原始载荷
	// 求值）；缺省沿用 legacy——finishReasonPath 字符串化非空、
	// status == "completed"。
	finishHit := response.StopReason != ""
	if decoder.finishWhen != nil {
		finishHit = customMatchEval(root, *decoder.finishWhen)
	}
	statusHit := response.Status == MaheshvaraStatusCompleted
	if decoder.statusWhen != nil {
		statusHit = customMatchEval(root, *decoder.statusWhen)
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

// matchJSONDoneValue 判定整帧载荷是否类型化等于任一配置的 done JSON 值。
func matchJSONDoneValue(root any, expectedValues []any) bool {
	for _, expected := range expectedValues {
		if customJSONValuesEqual(root, expected) {
			return true
		}
	}
	return false
}

// matchFrameIndex 选中首个匹配帧的下标（-1 表示未命中）：事件名与谓词（对
// 帧原始 JSON 求值）都给出时须同时成立；二者至少配一个（校验保证）。
// 返回下标而非拷贝,调用方直接引用构造期切片。
func (decoder *CustomProtocolStreamDecoder) matchFrameIndex(eventName string, root any, rootOK bool) int {
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
		return index
	}
	return -1
}

// effectiveEventName 取帧的事件名：优先 SSE event 字段，缺省时按 eventKeys
// （默认 type/event）回落已解析载荷内的字段（Responses 型协议把类型写在
// 数据里）。与旧 wireEventName/payloadEventName 双实现统一于此。
func (decoder *CustomProtocolStreamDecoder) effectiveEventName(wireEventName string, root any, rootOK bool) string {
	wireEventName = strings.TrimSpace(wireEventName)
	if wireEventName != "" || !rootOK {
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

// frameToolItems 从帧 JSON 组装工具调用增量（单帧可多工具）：身份帧
// （id/name 可得）注册 index→身份；参数帧经 index 关联或直接携带 id。仅身份
// 无参数时 Arguments 留空（不产生参数增量，避免 "{}" 混入拼装流）。tool.Path
// 指向工具数组时按元素遍历，各路径相对元素；否则整帧视为单工具。
func (decoder *CustomProtocolStreamDecoder) frameToolItems(tool *CustomProtocolStreamTool, root any) []MaheshvaraOutputItem {
	if tool == nil || root == nil {
		return nil
	}
	if strings.TrimSpace(tool.Path) == "" {
		if item, ok := decoder.buildFrameToolItem(tool, root); ok {
			return []MaheshvaraOutputItem{item}
		}
		return nil
	}
	var items []MaheshvaraOutputItem
	for _, element := range customArrayAt(root, tool.Path) {
		if item, ok := decoder.buildFrameToolItem(tool, element); ok {
			items = append(items, item)
		}
	}
	return items
}

func (decoder *CustomProtocolStreamDecoder) buildFrameToolItem(tool *CustomProtocolStreamTool, element any) (MaheshvaraOutputItem, bool) {
	id := customStringAt(element, tool.IDPath)
	name := customStringAt(element, tool.NamePath)
	index, hasIndex := 0.0, false
	if strings.TrimSpace(tool.IndexPath) != "" {
		if number, ok := numberValue(customValueAt(element, tool.IndexPath)); ok {
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
		Type: MaheshvaraOutputFunctionCall, Status: MaheshvaraStatusCompleted,
		CallID: id, Name: name,
	}
	if strings.TrimSpace(tool.ArgumentsPath) != "" {
		if arguments := customValueAt(element, tool.ArgumentsPath); arguments != nil {
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
func (decoder *CustomProtocolStreamDecoder) buildContentEvents(response *MaheshvaraResponse, argsMode string) []MaheshvaraStreamEvent {
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
			key := firstNonEmptyString(item.CallID, item.Name, fmt.Sprintf(customStreamKeyToolFmt, outputIndex))
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
			delta := decoder.streamDelta(decoder.previousReasoning, fmt.Sprintf(customStreamKeyReasoningFmt, outputIndex), text, decoder.modeReasoning)
			if delta != "" {
				events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ItemID: item.ID, ReasoningDelta: delta})
			}
		default:
			events = append(events, decoder.contentPartEvents(response, item, outputIndex)...)
		}
	}
	if response.Usage != nil {
		events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventUsageDelta, ResponseID: response.ID, Model: response.Model, Usage: response.Usage})
	}
	return events
}

// contentPartEvents 产生一条消息输出项的内容部件增量(文本/推理/拒费/其他),
// 自 buildContentEvents 的部件循环提取以压平嵌套。
func (decoder *CustomProtocolStreamDecoder) contentPartEvents(response *MaheshvaraResponse, item MaheshvaraOutputItem, outputIndex int) []MaheshvaraStreamEvent {
	var events []MaheshvaraStreamEvent
	for contentIndex, part := range item.Content {
		key := fmt.Sprintf(customStreamKeyTextFmt, outputIndex, contentIndex)
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
			delta := decoder.streamDelta(decoder.previousText, fmt.Sprintf(customStreamKeyRefusalFmt, outputIndex, contentIndex), part.Text, decoder.modeText)
			if delta != "" {
				events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventRefusalDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, RefusalDelta: delta})
			}
		default:
			partCopy := part
			events = append(events, MaheshvaraStreamEvent{Type: MaheshvaraEventContentPartAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, ContentPart: &partCopy})
		}
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

// ForEachBatch 以统一排水语义迭代解码批次:读循环、终态前快照、排水窗口与
// 终态后坏帧容忍等协议细节单点化于此,转发路径与设计器流式采样共用;
// 调用方只处理每批事件(terminalBeforeBatch 标识该批解码前是否已处终态,
// 终态后仅应保留 usage/错误语义)。回调返回错误立即中止并原样返回。
func (decoder *CustomProtocolStreamDecoder) ForEachBatch(ctx context.Context, reader *SSEEventReader, handleBatch func(wireEvent SSEEvent, events []MaheshvaraStreamEvent, terminalBeforeBatch bool) error) error {
	for {
		idle := DefaultSSEIdleTimeout
		if decoder.TerminalReceived() {
			// 终态后排水中:只等 usage 尾帧、错误帧与 doneValue,短窗防上游
			// finish 后不关连接导致 DefaultSSEIdleTimeout 级长挂起。
			idle = PostTerminalSSEIdleTimeout
		}
		wireEvent, hasMore, readErr := reader.Read(ctx, idle)
		if readErr != nil {
			if decoder.TerminalReceived() {
				return nil // 排水窗耗尽视为干净收尾
			}
			return readErr
		}
		if !hasMore {
			return nil
		}
		terminalBeforeBatch := decoder.TerminalReceived()
		events, done, decodeErr := decoder.Decode(wireEvent)
		if decodeErr != nil {
			if terminalBeforeBatch {
				return nil // 终态后的坏帧不推翻已完成的流
			}
			return decodeErr
		}
		if err := handleBatch(wireEvent, events, terminalBeforeBatch); err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
