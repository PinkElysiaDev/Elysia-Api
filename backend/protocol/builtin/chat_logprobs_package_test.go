package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageStreamLogprobOwners(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	const probabilities = `[{"token":"x","logprob":-0.1,"bytes":[120]}]`
	for _, field := range []string{"content", "refusal"} {
		t.Run(field, func(t *testing.T) {
			raws := []string{
				fmt.Sprintf(`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant",%q:"x"},"logprobs":{%q:%s}}]}`, field, field, probabilities),
				`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}`,
			}
			frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
			if len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != 1 || len(response.Content[0].Children[0].Metadata) != 1 {
				t.Fatal("token probabilities lost their generated item", response.Content)
			}
			metadata := response.Content[0].Children[0].Metadata[0]
			if metadata.Name != "logprobs" || metadata.Location != "content" || metadata.Update != "" || metadata.SourceCodec != Chat {
				t.Fatal("collected token probabilities retained a wire container or update instruction", metadata)
			}
			sameJSON(t, metadata.Value.Bytes(), probabilities)
			for _, event := range frames[0].Events {
				for _, delta := range event.Metadata {
					if event.Type != p.ItemDelta || delta.Update != "append" || delta.Path != "/choices/0/logprobs/"+field || !delta.Value.IsArray() {
						t.Fatal("source probability fragments are not item-owned array appends", event)
					}
				}
			}
			rebuilt := encodePackageStream(t, compiled, frames, p.Scope{}, false)
			_, observed := decodePackageStream(t, compiled, rebuilt, p.Scope{})
			if len(observed.Content) != 1 || observed.Content[0].Kind != p.MessageNode || len(observed.Content[0].Children) != 1 || observed.Content[0].Children[0].Kind != response.Content[0].Children[0].Kind || len(observed.Content[0].Children[0].Metadata) != 1 {
				t.Fatal("target changed probability ownership", observed.Content)
			}
			sameJSON(t, observed.Content[0].Children[0].Metadata[0].Value.Bytes(), probabilities)
			native := encodePackageStream(t, compiled, frames, p.Scope{}, true)
			if len(native) != len(raws) || native[0] != raws[0] || native[1] != raws[1] {
				t.Fatal("normalization changed native frame bytes", native)
			}
		})
	}
}

func TestChatPackageStreamLogprobSnapshots(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	first := testValue(t, `[{"token":"a","logprob":-1}]`)
	complete := testValue(t, `[{"token":"a","logprob":-1},{"token":"b","logprob":-2}]`)
	for _, kind := range []p.NodeKind{p.TextNode, p.RefusalNode} {
		t.Run(string(kind), func(t *testing.T) {
			metadata := p.ResponseMetadata{Codec: Chat, SourceCodec: Chat, Location: "content", Name: "logprobs", Path: "/probabilities", Value: first, Update: "append"}
			full := metadata
			full.Value, full.Update = complete, ""
			events := []p.Event{
				{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
				{Type: p.ItemStarted, ItemID: p.StringValue("item"), Item: &p.Node{Kind: kind}},
				{Type: p.ItemDelta, ItemID: p.StringValue("item"), Delta: p.StringValue("a"), Metadata: []p.ResponseMetadata{metadata}},
				{Type: p.ItemSnapshot, ItemID: p.StringValue("item"), Item: &p.Node{Kind: kind, Payload: p.StringValue("ab"), Metadata: []p.ResponseMetadata{full}}},
				{Type: p.ItemFinished, ItemID: p.StringValue("item"), Item: &p.Node{Kind: kind, Payload: p.StringValue("ab"), Metadata: []p.ResponseMetadata{full}}},
				{Type: p.ResponseFinished, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}},
			}
			wire := encodeChatMigrationEvents(t, compiled, streamEventsAtVersion(events, compiled.CoreVersion()), p.Scope{}, nil)
			var frames []p.Value
			if err := wire.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			raws := make([]string, len(frames))
			for index, frame := range frames {
				raws[index] = frame.JSONString()
			}
			_, response := decodePackageStream(t, compiled, raws, p.Scope{})
			if len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("ab") || len(response.Content[0].Children[0].Metadata) != 1 {
				t.Fatal("probability snapshots changed generated content", response.Content)
			}
			sameJSON(t, response.Content[0].Children[0].Metadata[0].Value.Bytes(), complete.JSONString())
		})
	}
}

func TestChatPackageNativeContinuationState(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	const firstProbability = `{"token":"a","logprob":-1}`
	const secondProbability = `{"token":"b","logprob":-2}`
	const annotation = `{"type":"url_citation","url_citation":{"start_index":0,"end_index":1,"title":"source","url":"https://example.test"}}`
	raws := []string{
		fmt.Sprintf(`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","service_tier":"default","choices":[{"index":0,"delta":{"role":"assistant","content":"a","annotations":[%s]},"logprobs":{"content":[%s]}}]}`, annotation, firstProbability),
		fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":"b","annotations":[%s]},"logprobs":{"content":[%s]}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`, annotation, secondProbability),
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"function":{"arguments":"{\"a\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"id":"call","function":{"name":"lookup","arguments":"1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
	}
	frames, _ := decodePackageStream(t, compiled, raws, p.Scope{})
	var capturedTool *p.Node
	for _, event := range frames[2].Events {
		if event.Item != nil && event.Item.Kind == p.ToolCallNode {
			capturedTool = event.Item
			break
		}
	}
	if capturedTool == nil || capturedTool.Source == nil || capturedTool.Source.Path != "/choices/0/delta/tool_calls/0" || capturedTool.Native == nil || capturedTool.Native.Source.Path != "/choices/0/delta/tool_calls/0/index" || capturedTool.Native.Value.JSONString() != "7" {
		t.Fatal("tool source and scalar wire-index ownership were conflated", capturedTool)
	}
	// A conversion may provide a cumulative probability snapshot after an
	// already-delivered native append. Only its new suffix belongs on the wire.
	for index := range frames[1].Events {
		for metadata := range frames[1].Events[index].Metadata {
			value := &frames[1].Events[index].Metadata[metadata]
			if value.Name == "logprobs" {
				value.Value = testValue(t, `[`+firstProbability+`,`+secondProbability+`]`)
				value.Update = ""
			}
		}
	}
	includeUsage := false
	options := p.EvaluationContext{State: p.NewEvaluationState(), ClientOutput: &p.ClientOutput{IncludeUsage: &includeUsage}}
	defer options.State.Release()
	var output []p.Value
	for index, frame := range frames {
		batch, err := compiled.EncodeConvertedFrame(t.Context(), frame, frame.Events, options)
		if err != nil {
			t.Fatal("mixed frame", index, err)
		}
		if index == 0 || index == 2 {
			if len(batch) != 1 || batch[0].JSONString() != raws[index] {
				t.Fatal("native frame changed", index, batch)
			}
		}
		output = append(output, batch...)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	output = append(output, tail...)
	encoded := make([]string, len(output))
	annotations, terminals := 0, 0
	for index, frame := range output {
		encoded[index] = frame.JSONString()
		var body struct {
			Usage   p.Value `json:"usage"`
			Choices []struct {
				FinishReason p.Value `json:"finish_reason"`
				Delta        struct {
					Annotations []p.Value `json:"annotations"`
					Tools       []struct {
						Index int `json:"index"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := frame.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Usage.IsZero() && !body.Usage.IsNull() {
			t.Fatal("mixed continuation exposed unrequested usage", frame)
		}
		for _, choice := range body.Choices {
			annotations += len(choice.Delta.Annotations)
			if choice.FinishReason.IsString("tool_calls") {
				terminals++
			}
			for _, tool := range choice.Delta.Tools {
				if tool.Index != 7 {
					t.Fatal("reconstruction changed the delivered tool index", frame)
				}
			}
		}
	}
	_, response := decodePackageStream(t, compiled, encoded, p.Scope{})
	if annotations != 1 || terminals != 1 || len(response.Content) != 1 || len(response.Content[0].Children) != 2 {
		t.Fatal("native continuation duplicated metadata, terminal or content", annotations, terminals, response.Content)
	}
	text, tool := response.Content[0].Children[0], response.Content[0].Children[1]
	if !text.Payload.IsString("ab") || tool.Input == nil || !tool.CallID.IsString("call") || !tool.Name.IsString("lookup") {
		t.Fatal("native continuation lost text or the late tool identity", response.Content)
	}
	sameJSON(t, tool.Input.Value.Bytes(), `{"a":1}`)
	hasProbabilities := false
	for _, metadata := range text.Metadata {
		if metadata.Name == "logprobs" {
			hasProbabilities = true
			sameJSON(t, metadata.Value.Bytes(), `[`+firstProbability+`,`+secondProbability+`]`)
		}
	}
	if !hasProbabilities {
		t.Fatal("native continuation dropped token probabilities")
	}
	request := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{tool}},
		{Kind: p.ToolResultNode, CallID: p.StringValue("call"), Name: p.StringValue("lookup"), Payload: p.StringValue("ok")},
	}}
	wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal("tool follow-up", err)
	}
	var followup struct {
		Messages []struct {
			Tools []struct {
				ID       string `json:"id"`
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := testValue(t, string(wire)).Decode(&followup); err != nil || len(followup.Messages) != 2 || len(followup.Messages[0].Tools) != 1 || followup.Messages[0].Tools[0].ID != "call" {
		t.Fatal("scalar index snapshot was replayed as tool history", string(wire), err)
	}
	sameJSON(t, []byte(followup.Messages[0].Tools[0].Function.Arguments), `{"a":1}`)
}
