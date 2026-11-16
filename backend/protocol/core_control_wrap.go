package protocol

import "fmt"

// ProtocolNodeWrapper declares presentation fields for a new message container.
// The selected node remains its sole child with unchanged payload and ownership.
type ProtocolNodeWrapper struct {
	ID     Expression `json:"id"`
	Status Expression `json:"status"`
}

type compiledNodeWrapper struct {
	id, status *compiledOutputPlan
}

func compileNodeWrapper(declaration ProtocolNodeWrapper, compiler *expressionCompiler, path string, scope expressionScope) (*compiledNodeWrapper, error) {
	id, err := compiler.compilePlan(declaration.ID, path+"/id", scope, 1)
	if err != nil {
		return nil, err
	}
	status, err := compiler.compilePlan(declaration.Status, path+"/status", scope, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(id.expression.result, StringType) || !isAssignable(status.expression.result, StringType) {
		return nil, fmt.Errorf("%s: wrapper identity and status must be strings", path)
	}
	return &compiledNodeWrapper{id: id, status: status}, nil
}

func (plan *compiledContentLayout) wrapNodes(state evaluation, content *[]Node, native **Native) (bool, error) {
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
		if node.Kind == MessageNode {
			return false, streamIssue(UnsupportedCapability, "/content", "wrapping requires a leaf node")
		}
		id, err := plan.wrapper.id.evaluate(local)
		if err != nil {
			return false, err
		}
		status, err := plan.wrapper.status.evaluate(local)
		if err != nil {
			return false, err
		}
		if !id.IsZero() && valueType(id) != StringType || !status.IsZero() && valueType(status) != StringType {
			return false, streamIssue(InvalidInput, "/content", "wrapper identity and status must be strings")
		}
		if output == nil {
			output = append(make([]Node, 0, len(*content)), (*content)[:index]...)
		}
		output = append(output, Node{Kind: MessageNode, Role: plan.role, ID: id, Status: status, Children: []Node{*node}})
	}
	if output == nil {
		return false, nil
	}
	*content, *native = output, nil
	return true, nil
}
