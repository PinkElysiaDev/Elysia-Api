package relay

import (
	"fmt"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

func newStreamState() *protocol.StreamState {
	return protocol.DefaultStreamState()
}

// validateStreamBatch is shared by wire modules and declared event mappings.
// Usage tails remain valid after terminal, while late content is a contract
// violation. Choice terminals are deferred until every choice has completed.
func validateStreamBatch(state *protocol.StreamState, events []MaheshvaraStreamEvent, isTerminal bool) error {
	for _, event := range events {
		if event.Error != nil || event.Type == MaheshvaraEventResponseFailed || event.Type == MaheshvaraEventResponseCompleted || event.Type == MaheshvaraEventUsageDelta {
			continue
		}
		if maheshvaraStreamEventHasOutput(event) {
			if err := state.CheckContent(); err != nil {
				return err
			}
		}
		key := fmt.Sprintf("tool/%d/%d", event.ChoiceIndex, event.ToolCallIndex)
		kind := protocol.JSONInput
		isAdded := event.Type == MaheshvaraEventFunctionCallAdded
		isDelta := event.Type == MaheshvaraEventFunctionCallArgumentsDelta
		isDone := event.Type == MaheshvaraEventFunctionCallArgumentsDone
		input := event.ToolArgumentsDelta
		if isDone {
			input = event.ToolArgumentsDone
		}
		if item := event.OutputItem; item != nil && (item.Type == MaheshvaraOutputFunctionCall || item.Type == "custom_tool_call") {
			key = fmt.Sprintf("tool/%d/%d", event.ChoiceIndex, event.OutputIndex)
			isAdded = event.Type == MaheshvaraEventOutputItemAdded
			isDone = event.Type == MaheshvaraEventOutputItemDone
			event.ToolCallID = item.CallID
			input = string(item.Arguments)
			if item.Type == "custom_tool_call" {
				kind, input = protocol.TextInput, item.Input
			}
		}
		if strings.HasPrefix(event.Type, "response.custom_tool_call_input.") {
			key = fmt.Sprintf("tool/%d/%d", event.ChoiceIndex, event.OutputIndex)
			kind = protocol.TextInput
			isDelta = strings.HasSuffix(event.Type, ".delta")
			isDone = strings.HasSuffix(event.Type, ".done")
			input = stringValue(event.Raw["delta"])
			if isDone {
				input = stringValue(event.Raw["input"])
			}
		}
		if !isAdded && !isDelta && !isDone {
			continue
		}
		if err := state.ObserveTool(key, event.ToolCallID, kind); err != nil {
			return err
		}
		if isDelta || isDone {
			if err := state.AppendTool(key, input, isDone, isDone); err != nil {
				return err
			}
		}
	}
	if isTerminal {
		return state.Finish()
	}
	return nil
}
