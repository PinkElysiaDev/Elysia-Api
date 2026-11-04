package server

import (
	"context"

	"github.com/elysia-api/backend/protocol"
)

// The preparation owner shares only digests of the immutable ingress history.
// Node positions, rather than equal contents, associate candidate-local copies.
type continuationHistory struct {
	nodes []continuationHistoryNode
	parts []continuationHistoryPart
	ends  []int
}

type continuationHistoryNode struct {
	digest   string
	children []continuationHistoryNode
}

type continuationHistoryPart struct{ role, digest string }

func (history *continuationHistory) prepare(ctx context.Context, nodes []protocol.Node) error {
	if history.nodes != nil {
		return nil
	}
	result := continuationHistory{nodes: make([]continuationHistoryNode, len(nodes)), ends: make([]int, len(nodes)+1)}
	var visit func(protocol.Node, string) (continuationHistoryNode, string, error)
	visit = func(node protocol.Node, role string) (continuationHistoryNode, string, error) {
		if err := ctx.Err(); err != nil {
			return continuationHistoryNode{}, role, err
		}
		if node.Kind == protocol.MessageNode {
			childRole := role
			_ = node.Role.Decode(&childRole)
			children := make([]continuationHistoryNode, len(node.Children))
			for index, child := range node.Children {
				var err error
				children[index], childRole, err = visit(child, childRole)
				if err != nil {
					return continuationHistoryNode{}, role, err
				}
			}
			return continuationHistoryNode{children: children}, role, nil
		}
		switch node.Kind {
		case protocol.ToolCallNode:
			role = "assistant"
		case protocol.ToolResultNode:
			role = "tool"
		}
		digest := protocol.ContinuationNodeDigest(node)
		result.parts = append(result.parts, continuationHistoryPart{role, digest})
		return continuationHistoryNode{digest: digest}, role, nil
	}
	role := ""
	for index, node := range nodes {
		result.ends[index] = len(result.parts)
		var err error
		result.nodes[index], role, err = visit(node, role)
		if err != nil {
			return err
		}
	}
	result.ends[len(nodes)] = len(result.parts)
	*history = result
	return nil
}

func (history *continuationHistory) parents(codec *protocol.ContinuationCodec) []string {
	digest := codec.NewHistoryDigest()
	parents := make([]string, len(history.ends))
	position := 0
	for index, end := range history.ends {
		for ; position < end; position++ {
			part := history.parts[position]
			digest.Append(part.role, part.digest)
		}
		parents[index] = digest.Sum()
	}
	return parents
}
