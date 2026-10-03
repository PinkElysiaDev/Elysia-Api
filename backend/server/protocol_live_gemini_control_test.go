package server

import (
	"encoding/json"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// Native history is a separate control: preserve the site's original function
// call, including absence of an ID, instead of rebuilding it from semantics.
func (suite *liveSuite) geminiNativeToolControl(t *testing.T, compiled *protocol.Compiled) {
	request := liveRequest(t, suite.Model, false)
	request.Content[0].Children[0].Payload = protocol.StringValue("Synthetic API verification. Return verify_echo with value 7; the client will supply its result.")
	request.Content[1].Children[0].Payload = protocol.StringValue("Call verify_echo with value 7.")
	request.Tools = []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("verify_echo"), InputSchema: mustProtocolValue(t, `{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}`)}}
	request.ToolChoice = mustProtocolValue(t, `{"mode":"function","name":"verify_echo"}`)
	first, _ := suite.direct(t, "tools/native-direct/call", compiled, request, false)
	suite.record(t, first)
	if first.Status != "passed" {
		t.Error("native tool call failed", first.Reason)
		return
	}
	body := buildGeminiNativeFollowup(t, first.Wire.request, first.Wire.body)
	second := liveCase{ID: "tools/native-direct/result", Target: compiled.Identity().DefinitionID, Revision: compiled.Hash(), Status: "failed", Wire: suite.exchange(t.Context(), compiled, body, false, nil)}
	if second.Wire.Status == 200 {
		if _, err := inspectLiveWire(compiled, &second); err != nil {
			second.Reason = err.Error()
		} else {
			second.Status = "passed"
		}
	} else {
		second.Reason = second.Wire.Error
	}
	suite.record(t, second)
	if second.Status != "passed" {
		t.Error("native tool result failed", second.Reason)
	}
}

type geminiControlCall struct {
	Name string          `json:"name"`
	ID   json.RawMessage `json:"id"`
	Args struct {
		Value int `json:"value"`
	} `json:"args"`
}

func buildGeminiNativeFollowup(t *testing.T, request, response []byte) []byte {
	t.Helper()
	var envelope struct {
		Candidates []struct {
			Content json.RawMessage `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Candidates) != 1 {
		t.Fatal("native control requires one candidate")
	}
	content := envelope.Candidates[0].Content
	var message struct {
		Parts []struct {
			Call *geminiControlCall `json:"functionCall"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(content, &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Parts) != 1 || message.Parts[0].Call == nil {
		t.Fatal("native control requires exactly one tool call")
	}
	call := message.Parts[0].Call
	if call.Name != "verify_echo" || call.Args.Value != 7 {
		t.Fatal("native control returned unexpected tool input")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request, &fields); err != nil {
		t.Fatal(err)
	}
	var history []json.RawMessage
	if err := json.Unmarshal(fields["contents"], &history); err != nil {
		t.Fatal(err)
	}
	resultFields := map[string]any{"name": call.Name, "response": map[string]int{"value": 7}}
	if len(call.ID) > 0 {
		resultFields["id"] = call.ID
	}
	result := mustEncodedProtocolValue(t, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": resultFields}}})
	history = append(history, content, result.Bytes())
	fields["contents"] = mustEncodedProtocolValue(t, history).Bytes()
	fields["toolConfig"] = []byte(`{"functionCallingConfig":{"mode":"NONE"}}`)
	return mustEncodedProtocolValue(t, fields).Bytes()
}

func TestGeminiNativeControlPreservesHistoryAndIDPresence(t *testing.T) {
	for _, id := range []string{"", `,"id":"original"`} {
		original := `{"role":"model","extension":9007199254740993,"parts":[{"functionCall":{"name":"verify_echo","args":{"value":7}` + id + `}}]}`
		wire := buildGeminiNativeFollowup(t, []byte(`{"contents":[{"role":"user","parts":[{"text":"call"}]}]}`), []byte(`{"candidates":[{"content":`+original+`}]}`))
		var followup struct {
			Contents []json.RawMessage `json:"contents"`
		}
		if err := json.Unmarshal(wire, &followup); err != nil {
			t.Fatal(err)
		}
		if len(followup.Contents) != 3 || string(followup.Contents[1]) != original {
			t.Fatal("native history changed")
		}
		var result struct {
			Parts []struct {
				Response map[string]json.RawMessage `json:"functionResponse"`
			} `json:"parts"`
		}
		if err := json.Unmarshal(followup.Contents[2], &result); err != nil {
			t.Fatal(err)
		}
		actual, hasID := result.Parts[0].Response["id"]
		if hasID != (id != "") || hasID && string(actual) != `"original"` {
			t.Fatal("native ID presence changed")
		}
	}
}
