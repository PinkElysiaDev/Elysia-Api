package protocol

import "strconv"

// groupRuns moves selected adjacent roots into a declared message container.
// Plain message containers can be unfolded; leaves keep their own provenance.
func (plan *compiledContentLayout) groupRuns(state evaluation, conversion *CompiledConversion, content *[]Node, native **Native, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	input := *content
	var output []Node
	start := -1
	flush := func(end int) error {
		if start < 0 {
			return nil
		}
		first := &input[start]
		if end == start+1 && first.Kind == MessageNode {
			if output != nil {
				output = append(output, *first)
			}
			start = -1
			return nil
		}
		if output == nil {
			output = make([]Node, start, len(input))
			copy(output, input[:start])
		}
		count := 0
		for index := start; index < end; index++ {
			if input[index].Kind == MessageNode {
				if !equalValues(input[index].Role, plan.role) || !isPlainContentContainer(input[index]) {
					return streamIssue(UnsupportedCapability, "/content/"+strconv.Itoa(index), "grouping requires a plain container with the declared role")
				}
				count += len(input[index].Children)
			} else {
				count++
			}
		}
		group := Node{Kind: MessageNode, Role: plan.role, Children: make([]Node, 0, count)}
		for index := start; index < end; index++ {
			node := &input[index]
			local := plan.local(state, node, index, true)
			if _, err := applyControlSteps(local, conversion, node, route, rule, sink, plan.steps, owner); err != nil {
				return err
			}
			if node.Kind == MessageNode {
				group.Children = append(group.Children, node.Children...)
			} else {
				group.Children = append(group.Children, *node)
			}
		}
		output = append(output, group)
		start = -1
		return nil
	}
	for index := range input {
		if err := state.advance(); err != nil {
			return false, err
		}
		node := &input[index]
		value, err := plan.when.evaluate(plan.local(state, node, index, false))
		if err != nil {
			return false, err
		}
		isSelected, err := readBoolean(value)
		if err != nil {
			return false, err
		}
		if isSelected {
			if start < 0 {
				start = index
			}
			continue
		}
		if err := flush(index); err != nil {
			return false, err
		}
		if output != nil {
			output = append(output, *node)
		}
	}
	if err := flush(len(input)); err != nil {
		return false, err
	}
	if output == nil {
		return false, nil
	}
	*content, *native = output, nil
	return true, nil
}
