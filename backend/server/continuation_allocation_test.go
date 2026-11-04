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
					if err := server.restoreGatewayContinuation(b.Context(), g, request, tokens); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
