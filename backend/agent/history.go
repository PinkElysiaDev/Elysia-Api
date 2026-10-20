package agent

import (
	"github.com/bytedance/sonic"
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

func toolResultTurn(info ToolResultInfo, limit int) conversationTurn {
	info = redactToolResult(validateToolResult(info))
	// The Agent owns this result envelope. Target protocols declare whether its
	// JSON value is submitted as an object or serialized text.
	fields := map[string]any{"ok": info.OK, "summary": info.Summary}
	if len(info.Data) > 0 {
		fields["data"] = clampJSON(info.Data, limit, ClampHead)
	}
	encoded, _ := sonic.ConfigStd.Marshal(fields) // Data has been validated at tool/persistence boundaries.
	payload, _ := protocol.ParseValue(encoded)
	return conversationTurn{Role: "tool", Content: []protocol.Node{{Kind: protocol.ToolResultNode, CallID: protocol.StringValue(info.CallID), Name: protocol.StringValue(info.Name), Payload: payload,
		Source: &protocol.Provenance{Protocol: protocol.AgentIdentity(), Direction: protocol.EncodeRequest}}}}
}
