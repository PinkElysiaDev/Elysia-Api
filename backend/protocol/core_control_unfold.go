package protocol

import "strconv"

// Selected children become roots. Unfolding splits plain containers into runs;
// detaching retains an already observable container and its remaining children.
func (plan *compiledContentLayout) relocateChildren(state evaluation, conversion *CompiledConversion, content *[]Node, native **Native, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	var output []Node
	for index := range *content {
		node := &(*content)[index]
		local := plan.local(state, node, index, false)
		value, err := plan.when.evaluate(local)
		if err != nil {
			return false, err
		}
		isSelected, err := readBoolean(value)
		if err != nil {
			return false, err
		}
		if !isSelected {
			if output != nil {
				output = append(output, *node)
			}
			continue
		}
		var selected []int
		for child := range node.Children {
			if err := state.advance(); err != nil {
				return false, err
			}
			value, err := plan.childWhen.evaluate(plan.local(state, &node.Children[child], index, true))
			if err != nil {
				return false, err
			}
			isSelected, err := readBoolean(value)
			if err != nil {
				return false, err
			}
			if isSelected {
				selected = append(selected, child)
			}
		}
		if len(selected) == 0 {
			if output != nil {
				output = append(output, *node)
			}
			continue
		}
		if node.Kind != MessageNode || plan.operation == "unfold_children" && !isPlainContentContainer(*node) {
			return false, streamIssue(UnsupportedCapability, "/content/"+strconv.Itoa(index), "unfolding requires explicit handling of container semantics")
		}
		if _, err := applyControlSteps(plan.local(state, node, index, true), conversion, node, route, rule, sink, plan.steps, owner); err != nil {
			return false, err
		}
		if output == nil {
			output = append(make([]Node, 0, len(*content)+len(selected)), (*content)[:index]...)
		}
		if plan.operation == "detach_children" {
			output = appendDetachedChildren(output, *node, selected)
		} else {
			output = appendUnfoldedChildren(output, *node, selected)
		}
	}
	if output == nil {
		return false, nil
	}
	*content, *native = output, nil
	return true, nil
}

func appendUnfoldedChildren(output []Node, node Node, selected []int) []Node {
	start := 0
	for _, child := range selected {
		if child > start {
			group := node
			group.Children, group.Native = node.Children[start:child:child], nil
			output = append(output, group)
		}
		output = append(output, node.Children[child])
		start = child + 1
	}
	if start < len(node.Children) {
		group := node
		group.Children, group.Native = node.Children[start:], nil
		output = append(output, group)
	}
	return output
}

func appendDetachedChildren(output []Node, node Node, selected []int) []Node {
	container := node
	container.Native, container.Children = nil, nil
	start := 0
	for _, child := range selected {
		container.Children = append(container.Children, node.Children[start:child]...)
		start = child + 1
	}
	container.Children = append(container.Children, node.Children[start:]...)
	output = append(output, container)
	for _, child := range selected {
		output = append(output, node.Children[child])
	}
	return output
}
