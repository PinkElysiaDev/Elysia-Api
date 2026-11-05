package server

import (
	"context"
	"fmt"
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
	g := &gatewayContinuation{codec: codec, owner: "fixture", scope: protocol.Scope{Provider: "provider"}, strict: true, restored: map[string]bool{}, settings: protocol.ContinuationSettings{ClientCarrier: true, RecordBytes: 1 << 20}}
	node := request.Content[7].Children[0]
	record := protocol.ContinuationRecord{Version: 1, ID: "fixture", Owner: g.owner, Scope: g.scope, Parent: g.historyDigest(request.Content[:7]), Digest: protocol.ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
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
	for _, changed := range []string{"owner", "scope", "content"} {
		t.Run(changed, func(t *testing.T) {
			candidate := *g
			candidate.restored = map[string]bool{}
			modified := request.Clone()
			switch changed {
			case "owner":
				candidate.owner = "other"
			case "scope":
				candidate.scope.Provider = "other"
			case "content":
				modified.Content[7].Children[0].Payload = protocol.StringValue("rewritten")
			}
			if err := (&Server{}).restoreGatewayContinuation(t.Context(), &candidate, modified, []string{token}, history); err == nil {
				t.Fatal("history cache bypassed candidate authentication")
			}
		})
	}
}

// Candidate preparation and retry attempts share only the immutable ingress
// digest table. Each attempt still authenticates its carrier and owns its copy.
func BenchmarkContinuationCandidateRecovery(b *testing.B) {
	codec, _ := protocol.NewContinuationCodec([]byte("isolated-candidate-key"))
	original := continuationHistoryFixture(64)
	owner := gatewayContinuation{codec: codec, owner: "candidate-fixture"}
	parent := owner.historyDigest(original.Content)
	node := original.Content[63].Children[0]
	record := protocol.ContinuationRecord{Version: 1, ID: "fixture", Owner: owner.owner, Parent: owner.historyDigest(original.Content[:63]), Digest: protocol.ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	token, err := codec.Seal(record, 1<<20)
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{1, 4} {
		for _, hasCarrier := range []bool{false, true} {
			b.Run(fmt.Sprintf("candidates-%d/carrier-%t", count, hasCarrier), func(b *testing.B) {
				var tokens []string
				if hasCarrier {
					tokens = []string{token}
				}
				server := &Server{}
				b.ReportAllocs()
				for b.Loop() {
					history := &continuationHistory{}
					for range count {
						candidate := gatewayContinuation{codec: codec, owner: owner.owner, strict: true, restored: map[string]bool{}, settings: protocol.ContinuationSettings{ClientCarrier: true, RecordBytes: 1 << 20}}
						if err := server.restoreGatewayContinuation(b.Context(), &candidate, original.Clone(), tokens, history); err != nil {
							b.Fatal(err)
						}
						candidate.prepareParent(original.Content)
						if candidate.parent != parent || hasCarrier && !candidate.restored[record.Digest] {
							b.Fatal("candidate lost its authenticated parent or node")
						}
					}
				}
			})
		}
	}
}
