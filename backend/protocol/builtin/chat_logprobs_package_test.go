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
