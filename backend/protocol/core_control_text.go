package protocol

import (
	"strconv"
	"strings"
)

type textLayoutPart struct {
	index  int
	text   string
	length int
}

func (plan *compiledContentLayout) mergeText(state evaluation, conversion *CompiledConversion, content *[]Node, native **Native, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	hasChanges := false
	for index := range *content {
		message := &(*content)[index]
		if message.Kind != MessageNode {
			continue
		}
		if plan.text.messageWhen != nil {
			value, err := plan.text.messageWhen.evaluate(plan.textLocal(state, message, "/content/"+strconv.Itoa(index), 0, 0))
			if err != nil {
				return false, err
			}
			isSelected, err := readBoolean(value)
			if err != nil {
				return false, err
			}
			if !isSelected {
				continue
			}
		}
		children, changed, err := plan.mergeTextChildren(state, conversion, message.Children, "/content/"+strconv.Itoa(index)+"/children", route, rule, sink, owner)
		if err != nil {
			return false, err
		}
		if changed {
			message.Children, message.Native = children, nil
			hasChanges = true
		}
	}
	if hasChanges {
		*native = nil
	}
	return hasChanges, nil
}

func (plan *compiledContentLayout) textLocal(state evaluation, node *Node, path string, offset, length int) evaluation {
	state.input, state.objectReader = Value{}, nil
	state.inputReader = func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer) }
	if plan.pathSlot >= 0 {
		state.bindings[plan.pathSlot] = JSONLeaf(StringValue(path))
	}
	if plan.text.offsetSlot >= 0 {
		state.bindings[plan.text.offsetSlot] = JSONLeaf(coreInteger(int64(offset)))
	}
	if plan.text.lengthSlot >= 0 {
		state.bindings[plan.text.lengthSlot] = JSONLeaf(coreInteger(int64(length)))
	}
	return state
}

func (plan *compiledContentLayout) mergeTextChildren(state evaluation, conversion *CompiledConversion, input []Node, path string, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) ([]Node, bool, error) {
	first := -1
	var parts []textLayoutPart
	for index := range input {
		if err := state.advance(); err != nil {
			return nil, false, err
		}
		node := &input[index]
		local := plan.textLocal(state, node, path+"/"+strconv.Itoa(index), 0, 0)
		value, err := plan.when.evaluate(local)
		if err != nil {
			return nil, false, err
		}
		isSelected, err := readBoolean(value)
		if err != nil {
			return nil, false, err
		}
		if !isSelected {
			continue
		}
		if first < 0 {
			first = index
			continue
		}
		if !plan.text.shouldCross && index != first+max(1, len(parts)) {
			return nil, false, streamIssue(UnsupportedCapability, path+"/"+strconv.Itoa(index), "text merging cannot cross an unselected node")
		}
		if parts == nil {
			parts = append(parts, textLayoutPart{index: first})
		}
		parts = append(parts, textLayoutPart{index: index})
	}
	if len(parts) == 0 {
		return input, false, nil
	}
	bytes, err := plan.measureTextParts(state, input, parts, path)
	if err != nil {
		return nil, false, err
	}
	var payload strings.Builder
	payload.Grow(bytes)
	merged := input[first]
	merged.Metadata, merged.Native = nil, nil
	offset := 0
	for _, part := range parts {
		node := &input[part.index]
		local := plan.textLocal(state, node, path+"/"+strconv.Itoa(part.index), offset, part.length)
		if _, err := applyControlSteps(local, conversion, node, route, rule, sink, plan.steps, owner); err != nil {
			return nil, false, err
		}
		for _, metadata := range node.Metadata {
			if err := state.advance(); err != nil {
				return nil, false, err
			}
			program := plan.text.metadata[metadata.Name]
			if program == nil {
				return nil, false, streamIssue(UnsupportedCapability, metadata.Path, "text metadata has no declared aggregation")
			}
			local.inputReader = func(pointer []string) (JSONOutput, error) { return readCoreResponseMetadata(metadata, pointer) }
			value, err := program.build(local)
			if err != nil {
				return nil, false, err
			}
			if value.output.IsZero() {
				return nil, false, streamIssue(UnsupportedCapability, metadata.Path, "text aggregation cannot silently omit metadata")
			}
			if err := assignCoreValue(value.output, &metadata.Value, state.valueReader); err != nil {
				return nil, false, err
			}
			merged.Metadata = append(merged.Metadata, metadata)
		}
		payload.WriteString(part.text)
		offset += part.length
	}
	merged.Payload = StringValue(payload.String())
	output := make([]Node, 0, len(input)-len(parts)+1)
	output = append(output, input[:first]...)
	output = append(output, merged)
	selected := 1
	for index := first + 1; index < len(input); index++ {
		if selected < len(parts) && index == parts[selected].index {
			selected++
			continue
		}
		output = append(output, input[index])
	}
	return output, true, nil
}

func (plan *compiledContentLayout) measureTextParts(state evaluation, input []Node, parts []textLayoutPart, path string) (int, error) {
	bytes := 0
	kind := input[parts[0].index].Kind
	for index := range parts {
		part := &parts[index]
		node := &input[part.index]
		at := path + "/" + strconv.Itoa(part.index)
		if node.Kind != kind || kind != TextNode && kind != RefusalNode && kind != ReasoningNode || !isPlainTextLeaf(*node) {
			return 0, streamIssue(UnsupportedCapability, at, "text merging requires equally typed leaves with explicitly handled attached semantics")
		}
		var err error
		part.text, err = readComparisonText(JSONLeaf(node.Payload))
		if err != nil {
			return 0, err
		}
		if len(part.text) > state.budget.limits.BufferBytes-bytes {
			return 0, resourceFailure("text layout", "bytes", state.budget.limits.BufferBytes, bytes+len(part.text))
		}
		bytes += len(part.text)
		measurement, err := plan.text.measure.evaluate(plan.textLocal(state, node, at, 0, 0))
		if err != nil {
			return 0, err
		}
		if err := measurement.Decode(&part.length); err != nil || part.length < 0 || part.length > len(part.text)*2 {
			return 0, streamIssue(InvalidInput, at, "text measurement must be a bounded nonnegative length")
		}
	}
	return bytes, nil
}

func isPlainTextLeaf(node Node) bool {
	node.Payload, node.Metadata = Value{}, nil
	return isPlainContentContainer(node) && node.Role.IsZero() && len(node.Children) == 0
}

func utf16TextLength(text string) int {
	length := 0
	for _, character := range text {
		length++
		if character >= 0x10000 {
			length++
		}
	}
	return length
}
