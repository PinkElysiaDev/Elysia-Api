package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const responseRangeAssociation = "response"

// Association keys describe original owners, independently of target IDs.
// They are private receipts, exposed only by the read-only range projection.
func makeRangeNodeIdentity(parent string, node Node, index int) string {
	identity := "index:" + strconv.Itoa(index)
	if !node.ID.IsZero() && !node.ID.IsNull() {
		var name string
		if node.ID.Decode(&name) == nil {
			identity = "id:" + name
		} else {
			identity = "value:" + node.ID.raw
		}
	}
	digest := sha256.Sum256([]byte(parent + "\x00" + identity))
	return hex.EncodeToString(digest[:])
}

func readRangeNodeAssociation(parent string, node Node, index int) string {
	if node.rangeAssociation != "" {
		return node.rangeAssociation
	}
	return makeRangeNodeIdentity(parent, node, index)
}

func visitRangeAssociations(nodes []Node, parent string, visit func(*Node, string)) {
	for index := range nodes {
		node := &nodes[index]
		key := makeRangeNodeIdentity(parent, *node, index)
		visit(node, key)
		visitRangeAssociations(node.Children, key+"/children", visit)
		visitRangeAssociations(node.ReasoningContent, key+"/reasoningContent", visit)
	}
}

func selectRangeResponse(document any) *Response {
	switch current := document.(type) {
	case *Response:
		return current
	case *Event:
		return current.Response
	default:
		return nil
	}
}

func bindRangeAssociations(document any) {
	response := selectRangeResponse(document)
	if response == nil || !hasRangeMetadata(response.Content) {
		return
	}
	visitRangeAssociations(response.Content, responseRangeAssociation+"/content", func(node *Node, key string) {
		if node.rangeAssociation == "" {
			node.rangeAssociation = key
		}
	})
}

func hasRangeMetadata(nodes []Node) bool {
	for _, node := range nodes {
		if len(node.Metadata) != 0 || hasRangeMetadata(node.Children) || hasRangeMetadata(node.ReasoningContent) {
			return true
		}
	}
	return false
}

func captureRangeAssociations(document any) map[string]string {
	response := selectRangeResponse(document)
	if response == nil {
		return nil
	}
	var bindings map[string]string
	visitRangeAssociations(response.Content, responseRangeAssociation+"/content", func(node *Node, key string) {
		if node.rangeAssociation == "" {
			return
		}
		if bindings == nil {
			bindings = map[string]string{}
		}
		if _, exists := bindings[key]; exists {
			bindings[key] = "" // Ambiguous authored identity grants no original owner.
		} else {
			bindings[key] = node.rangeAssociation
		}
	})
	return bindings
}

func restoreRangeAssociations(document any, bindings map[string]string) {
	response := selectRangeResponse(document)
	if response == nil || len(bindings) == 0 {
		return
	}
	visitRangeAssociations(response.Content, responseRangeAssociation+"/content", func(node *Node, key string) {
		node.rangeAssociation = bindings[key]
	})
}
