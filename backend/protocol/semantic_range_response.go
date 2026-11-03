package protocol

import (
	"context"
	"slices"
)

// Reference selectors see content and identity, never native evidence, resource
// authority or delivery receipts. Recursive children retain the same projection.
var semanticRangeResponseSchema = func() ValueSchema {
	node := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{
		"id": {Type: AnyType}, "kind": {Type: StringType}, "payload": {Type: AnyType},
		"metadata": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}},
	}}
	node.Properties["children"] = ValueSchema{Type: ArrayType, Items: node}
	node.Properties["reasoningContent"] = ValueSchema{Type: ArrayType, Items: node}
	return ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{
		"id": {Type: AnyType}, "content": {Type: ArrayType, Items: node},
		"metadata": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}},
	}}
}()

func inheritResponseRangeOrigin(before, after *Response) {
	if after == nil {
		return
	}
	if before == nil {
		after.rangeOrigin = &Response{}
		return
	}
	if before.SchemaVersion != ProtocolCoreVersion {
		return
	}
	after.rangeOrigin = before.rangeOrigin
	if after.rangeOrigin == nil && !sameResponseRangeInput(before, after) {
		after.rangeOrigin = &Response{ID: before.ID, Metadata: slices.Clone(before.Metadata), Content: captureRangeNodes(before.Content)}
	}
}

func captureRangeNodes(nodes []Node) []Node {
	if nodes == nil {
		return nil
	}
	result := make([]Node, len(nodes))
	for i, node := range nodes {
		result[i] = Node{ID: node.ID, Kind: node.Kind, Payload: node.Payload, Metadata: slices.Clone(node.Metadata),
			Children: captureRangeNodes(node.Children), ReasoningContent: captureRangeNodes(node.ReasoningContent)}
	}
	return result
}

func sameResponseRangeInput(before, after *Response) bool {
	return before.ID == after.ID && slices.Equal(before.Metadata, after.Metadata) && sameRangeNodes(before.Content, after.Content)
}

func sameRangeNodes(before, after []Node) bool {
	if len(before) != len(after) {
		return false
	}
	for i, node := range before {
		other := after[i]
		if node.ID != other.ID || node.Kind != other.Kind || node.Payload != other.Payload || !slices.Equal(node.Metadata, other.Metadata) ||
			!sameRangeNodes(node.Children, other.Children) || !sameRangeNodes(node.ReasoningContent, other.ReasoningContent) {
			return false
		}
	}
	return true
}

func readSemanticRangeResponse(response *Response, pointer []string) (JSONOutput, error) {
	if len(pointer) == 0 {
		return readRangeObject([]string{"id", "content", "metadata"}, func(path []string) (JSONOutput, error) {
			return readSemanticRangeResponse(response, path)
		})
	}
	switch pointer[0] {
	case "content":
		return readRangeNodes(response.Content, pointer[1:])
	case "id", "metadata":
		return readCoreResponseOutput(response, pointer)
	default:
		return JSONOutput{}, nil
	}
}

func readRangeNodes(nodes []Node, pointer []string) (JSONOutput, error) {
	if len(pointer) != 0 {
		index, err := coreArrayIndex(pointer[0], len(nodes))
		if err != nil || index < 0 {
			return JSONOutput{}, err
		}
		return readRangeNode(nodes[index], pointer[1:])
	}
	items := make([]JSONOutput, len(nodes))
	for i, node := range nodes {
		value, err := readRangeNode(node, nil)
		if err != nil {
			return JSONOutput{}, err
		}
		items[i] = value
	}
	return JSONOutput{node: &jsonOutputNode{items: items}}, nil
}

func readRangeNode(node Node, pointer []string) (JSONOutput, error) {
	if len(pointer) == 0 {
		return readRangeObject([]string{"id", "kind", "payload", "metadata", "children", "reasoningContent"}, func(path []string) (JSONOutput, error) {
			return readRangeNode(node, path)
		})
	}
	switch pointer[0] {
	case "children":
		return readRangeNodes(node.Children, pointer[1:])
	case "reasoningContent":
		return readRangeNodes(node.ReasoningContent, pointer[1:])
	case "id", "kind", "payload", "metadata":
		return readCoreNode(node, pointer)
	default:
		return JSONOutput{}, nil
	}
}

func readRangeObject(names []string, read func([]string) (JSONOutput, error)) (JSONOutput, error) {
	fields := make([]jsonOutputField, 0, len(names))
	for _, name := range names {
		value, err := read([]string{name})
		if err != nil {
			return JSONOutput{}, err
		}
		if !value.IsZero() {
			fields = append(fields, jsonOutputField{name, value})
		}
	}
	return JSONOutput{node: &jsonOutputNode{object: true, fields: fields}}, nil
}

func responseRangeEvaluation(ctx context.Context, response *Response, limits Limits) evaluation {
	read := func(pointer []string) (JSONOutput, error) { return readSemanticRangeResponse(response, pointer) }
	return evaluation{ctx: ctx, inputReader: read, rootReader: read, valueReader: &valueReader{},
		budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}}
}

// ValidateResponseReferences checks core reference associations independently of
// wire encoding. Agent collection uses the same boundary as client responses.
func (compiled *Compiled) ValidateResponseReferences(ctx context.Context, response *Response) error {
	declarations := compiled.mappings[EncodeResponse].semanticRanges
	if len(declarations) == 0 || response == nil {
		return nil
	}
	before := response.rangeOrigin
	if before == nil {
		before = response
	}
	prior := responseRangeEvaluation(ctx, before, compiled.limits)
	after := prior
	if before != response {
		after = responseRangeEvaluation(ctx, response, compiled.limits)
	}
	if err := checkResponseRangeDeclarations(declarations, prior, after, before == response); err != nil {
		return compiled.mappingError(EncodeResponse, err)
	}
	return nil
}
