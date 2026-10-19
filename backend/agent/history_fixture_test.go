package agent

import (
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// buildAssistantFixture builds canonical nodes for the fake model and test store.
func buildAssistantFixture(content *AssistantContent) error {
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
		if err != nil || !arguments.IsObject() || call.ID == "" || call.Name == "" {
			return fmt.Errorf("invalid fixture call %q", call.ID)
		}
		children = append(children, protocol.Node{Kind: protocol.ToolCallNode, CallID: protocol.StringValue(call.ID), Name: protocol.StringValue(call.Name), Input: &protocol.ToolInput{Kind: protocol.JSONInput, Value: arguments}})
	}
	content.Content = []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue(RoleAssistant), Children: children}}
	return nil
}
