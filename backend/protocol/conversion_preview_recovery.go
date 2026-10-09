package protocol

import (
	"crypto/rand"
	"slices"
	"time"
)

// PreviewRecovery proves authenticated fragment recovery in memory. It never
// claims a production persistence write, and never attaches synthetic provider
// signatures. Only the preview/verification entrypoints call this method.
func (c *CompiledConversion) PreviewRecovery(phase ConversionPhase, input Value, route ConversionContext) (ConversionContext, error) {
	if c == nil || c.Policy.Continuation == nil || !c.SignatureProjectionEnabled(phase, route) ||
		(!c.Policy.Continuation.ClientCarrier && !c.Policy.Continuation.Persist) {
		return route, nil
	}
	var nodes []Node
	switch phase {
	case ConversionResponse:
		var response Response
		if err := input.Decode(&response); err != nil {
			return route, err
		}
		nodes = response.Content
	case ConversionEvent:
		var event Event
		if err := input.Decode(&event); err != nil {
			return route, err
		}
		if event.Item != nil {
			nodes = append(nodes, *event.Item)
		}
		if event.Response != nil {
			nodes = append(nodes, event.Response.Content...)
		}
	default:
		return route, nil
	}
	codec, err := NewContinuationCodec(randBytesForPreview())
	if err != nil {
		return route, err
	}
	route.Recoverable = map[string]bool{}
	var visit func([]Node) error
	visit = func(nodes []Node) error {
		for _, node := range nodes {
			if HasSignature(node) {
				record := ContinuationRecord{Version: 1, ID: "offline-proof", Owner: "offline-proof", Protocol: route.Source, Scope: route.Scope, Digest: ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
				token, err := codec.Seal(record, c.Policy.Continuation.RecordBytes)
				if err != nil {
					return err
				}
				opened, err := codec.Open(token, c.Policy.Continuation.RecordBytes)
				if err != nil {
					return err
				}
				copy := node
				copy.Resources = slices.DeleteFunc(slices.Clone(node.Resources), func(r Resource) bool { return r.Kind == "signature" })
				if err := RestoreContinuation(&copy, opened, record.Owner, route.Scope, route.Source); err != nil {
					return err
				}
				if !HasSignature(copy) {
					return streamIssue(VerificationMismatch, "/continuation", "authenticated fragment did not restore signatures")
				}
				route.Recoverable[ContinuationNodeDigest(node)] = true
				for _, r := range node.Resources {
					if r.Kind == "signature" {
						route.Recoverable[SignatureRecoveryKey(r)] = true
					}
				}
			}
			if err := visit(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	err = visit(nodes)
	return route, err
}

func randBytesForPreview() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return key
}
