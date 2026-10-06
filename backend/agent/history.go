package agent

import (
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// conversationTurn keeps persistence and compaction boundaries without
// creating a second message/tool representation for protocol conversion.
type conversationTurn struct {
	Role    string
	Content []protocol.Node
}

func textTurn(role, text string) conversationTurn {
	return conversationTurn{Role: role, Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue(role), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(text)}}}}}
}

func flattenConversation(conversation []conversationTurn) []protocol.Node {
	var content []protocol.Node
	for _, turn := range conversation {
		content = append(content, turn.Content...)
	}
	return content
}

func assistantTurn(content AssistantContent) conversationTurn {
	return conversationTurn{Role: RoleAssistant, Content: content.Content}
}

// importAssistantContent upgrades historical display-only rows. It cannot
// reconstruct signatures or encrypted reasoning that old releases discarded.
func importAssistantContent(content *AssistantContent) error {
	if content.Content != nil {
		return nil
	}
	var children []protocol.Node
	if content.Reasoning != "" {
		children = append(children, protocol.Node{Kind: protocol.ReasoningNode, Payload: protocol.StringValue(content.Reasoning)})
	}
	if content.Text != "" {
		children = append(children, protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue(content.Text)})
	}
	for _, call := range content.ToolCalls {
		arguments, err := protocol.ParseValue(call.Arguments)
		if err != nil || !arguments.IsObject() || call.ID == "" || call.Name == "" || (call.Type != "" && call.Type != "function") {
			return fmt.Errorf("historical Agent call %q requires an identity, function name and JSON object arguments", call.ID)
		}
		children = append(children, protocol.Node{Kind: protocol.ToolCallNode, CallID: protocol.StringValue(call.ID), Name: protocol.StringValue(call.Name), Input: &protocol.ToolInput{Kind: protocol.JSONInput, Value: arguments}})
	}
	content.Content = []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue(RoleAssistant), Children: children}}
	return nil
}

func toolResultTurn(info ToolResultInfo, limit int) conversationTurn {
	// The Agent owns this result envelope. Target protocols declare whether its
	// JSON value is submitted as an object or serialized text.
	fields := map[string]any{"ok": info.OK, "summary": info.Summary}
	if len(info.Data) > 0 {
		fields["data"] = clampJSON(info.Data, limit, ClampHead)
	}
	encoded, _ := json.Marshal(fields) // Data has been validated at tool/persistence boundaries.
	payload, _ := protocol.ParseValue(encoded)
	return conversationTurn{Role: "tool", Content: []protocol.Node{{Kind: protocol.ToolResultNode, CallID: protocol.StringValue(info.CallID), Name: protocol.StringValue(info.Name), Payload: payload,
		Source: &protocol.Provenance{Protocol: protocol.AgentIdentity(), Direction: protocol.EncodeRequest}}}}
}
