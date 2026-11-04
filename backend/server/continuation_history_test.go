package server

import (
	"context"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestContinuationHistoryPrefixes(t *testing.T) {
	codec, _ := protocol.NewContinuationCodec([]byte("isolated-history-key"))
	g := &gatewayContinuation{codec: codec}
	text := func(value string) protocol.Node {
		return protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue(value)}
	}
	for _, nodes := range [][]protocol.Node{
		nil,
		continuationHistoryFixture(64).Content,
		{
			{Kind: protocol.ToolCallNode, CallID: protocol.StringValue("c"), Payload: protocol.StringValue("arguments")},
			text("inherits assistant"),
			{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{
				text("user"),
				{Kind: protocol.MessageNode, Children: []protocol.Node{text("inherits user")}},
				{Kind: protocol.ToolResultNode, CallID: protocol.StringValue("c"), Payload: protocol.StringValue("result")},
				text("inherits tool"),
			}},
			text("still assistant outside message"),
			{Kind: protocol.MessageNode, Role: protocol.StringValue("assistant")},
		},
	} {
		history := &continuationHistory{}
		if err := history.prepare(t.Context(), nodes); err != nil {
			t.Fatal(err)
		}
		parents := history.parents(codec)
		for index := 0; index <= len(nodes); index++ {
			if parents[index] != g.historyDigest(nodes[:index]) {
				t.Fatal("prefix association changed", index)
			}
		}
	}
}

func TestContinuationHistoryDoesNotPublishCancelledWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	history := &continuationHistory{}
	if err := history.prepare(ctx, continuationHistoryFixture(8).Content); err != context.Canceled {
		t.Fatal(err)
	}
	if history.nodes != nil || history.parts != nil || history.ends != nil {
		t.Fatal("cancelled history retained partial work")
	}
}

func TestContinuationHistorySharesOnlyPreparationDigests(t *testing.T) {
	codec, _ := protocol.NewContinuationCodec([]byte("isolated-history-key"))
	history := &continuationHistory{}
	request := continuationHistoryFixture(8)
	if err := history.prepare(t.Context(), request.Content); err != nil {
		t.Fatal(err)
	}
	g := &gatewayContinuation{codec: codec, strict: true, restored: map[string]bool{}, settings: protocol.ContinuationSettings{ClientCarrier: true, RecordBytes: 1 << 20}}
	node := request.Content[7].Children[0]
	record := protocol.ContinuationRecord{Version: 1, ID: "fixture", Parent: g.historyDigest(request.Content[:7]), Digest: protocol.ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	token, err := codec.Seal(record, g.settings.RecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		candidate := *g
		if err := (&Server{}).restoreGatewayContinuation(t.Context(), &candidate, request.Clone(), []string{token}, history); err != nil {
			t.Fatal(err)
		}
		candidate.prepareParent(request.Content)
		if candidate.parent != g.historyDigest(request.Content) {
			t.Fatal("candidate parent changed")
		}
	}
}
