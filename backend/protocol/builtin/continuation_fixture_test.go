package builtin

import (
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

// fixtureContinuation supplies authenticated persistence evidence, as the host
// does after sealing a source node; ordinary JSON cannot claim recovery.
func fixtureContinuation(t testing.TB, target *p.Compiled, source p.Identity, scope p.Scope, nodes []p.Node) *p.ContinuationBinding {
	t.Helper()
	codec, err := p.NewContinuationCodec([]byte("synthetic continuation fixture"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := target.BindContinuation(p.EncodeResponse, source, scope, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(binding.Release)
	for ordinal, node := range nodes {
		state, err := codec.SealState(p.ContinuationRecord{Version: 1, Ordinal: ordinal, Protocol: source, Scope: scope, Node: node, Digest: p.ContinuationNodeDigest(node), ExpiresAt: time.Now().Add(time.Hour).Unix()}, target.ResourceLimits().BufferBytes)
		if err != nil {
			t.Fatal(err)
		}
		state.MarkPersisted()
		if err := binding.Append(state); err != nil {
			t.Fatal(err)
		}
	}
	return binding
}
