package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func continuationHistoryFixture(count int) *protocol.Request {
	request := &protocol.Request{}
	for index := range count {
		role := "user"
		if index%2 != 0 {
			role = "assistant"
		}
		request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue(role), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(strings.Repeat("x", 4096))}}})
	}
	return request
}

func TestContinuationRecoveryEligibility(t *testing.T) {
	codec, err := protocol.NewContinuationCodec([]byte("isolated-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                                          string
		persist, hasSession, hasKey, hasCarrier, want bool
	}{
		{"none", false, false, true, false, false},
		{"no-session", true, false, true, false, false},
		{"no-persistence", false, true, true, false, false},
		{"persistent", true, true, true, false, true},
		{"missing-key", true, true, false, false, false},
		{"carrier-without-key", false, false, false, true, true},
		{"carrier", false, false, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := &gatewayContinuation{isPackage: true, settings: protocol.ContinuationSettings{Persist: test.persist}}
			if test.hasSession {
				g.session = "session"
			}
			if test.hasKey {
				g.codec = codec
			}
			var tokens []string
			if test.hasCarrier {
				tokens = []string{"invalid"}
			}
			if g.needsRecovery(tokens) != test.want {
				t.Fatal("recovery eligibility changed")
			}
			if !test.want {
				// No store is available: an unnecessary persistent lookup would fail.
				if err := (&Server{}).restoreGatewayContinuation(t.Context(), g, continuationHistoryFixture(64), nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if test.hasCarrier {
				if err := (&Server{}).restoreGatewayContinuation(t.Context(), g, continuationHistoryFixture(8), tokens, nil); err == nil {
					t.Fatal("invalid carrier bypassed authentication")
				}
			}
		})
	}
}

func TestContinuationParentKeepsOriginalHistory(t *testing.T) {
	codec, _ := protocol.NewContinuationCodec([]byte("isolated-test-key"))
	g := &gatewayContinuation{codec: codec, settings: protocol.ContinuationSettings{ClientCarrier: true}}
	request := continuationHistoryFixture(8)
	want := g.historyDigest(request.Content)
	g.prepareParent(request.Content)
	request.Content[0].Children[0].Payload = protocol.StringValue("changed after preparation")
	if g.parent != want {
		t.Fatal("delivery parent changed with released preparation")
	}
}

func BenchmarkContinuationRecovery(b *testing.B) {
	codec, err := protocol.NewContinuationCodec([]byte("isolated-benchmark-key"))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{8, 32, 64, 128} {
		original := continuationHistoryFixture(count)
		for _, hasCarrier := range []bool{false, true} {
			b.Run(fmt.Sprintf("messages-%d/carrier-%t", count, hasCarrier), func(b *testing.B) {
				g := &gatewayContinuation{codec: codec, owner: "fixture", strict: true, sink: &protocol.DiagnosticSink{}, settings: protocol.ContinuationSettings{RecordBytes: 1 << 20}}
				var tokens []string
				if hasCarrier {
					node := original.Content[count-1].Children[0]
					record := protocol.ContinuationRecord{Version: 1, ID: "fixture", Owner: g.owner, Parent: g.historyDigest(original.Content[:count-1]), Digest: protocol.ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
					token, err := codec.Seal(record, g.settings.RecordBytes)
					if err != nil {
						b.Fatal(err)
					}
					tokens = []string{token}
				}
				server := &Server{}
				b.ReportAllocs()
				for b.Loop() {
					request := original.Clone()
					g.restored = map[string]bool{}
					if err := server.restoreGatewayContinuation(b.Context(), g, request, tokens, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
