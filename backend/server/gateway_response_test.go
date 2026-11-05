package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayResponseCarrierCompletion(t *testing.T) {
	document := agentEnvelopePackage(t)
	document.Spec.Documents["response"].Schema.Properties["carrier"] = protocol.ValueSchema{Type: protocol.ArrayType, Items: &protocol.ValueSchema{Type: protocol.StringType}}
	var declaration protocol.ProtocolContinuationMapping
	if err := mustProtocolValue(t, `{"encode_response":{"program":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"object","fields":{"carrier":{"op":"map","source":{"op":"read","from":"runtime","path":"/continuation/tokens"},"body":{"op":"read","from":"item","path":"/token"}}}}]},"emitted":{"op":"read","path":"/carrier"}}}`).Decode(&declaration); err != nil {
		t.Fatal(err)
	}
	document.Mapping.Continuation = &declaration
	compiled := compileSourceStreamPackage(t, document)
	codec, err := protocol.NewContinuationCodec([]byte("response carrier fixture"))
	if err != nil {
		t.Fatal(err)
	}
	node := protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue("hi")}
	sealed, err := codec.SealState(protocol.ContinuationRecord{Version: 1, ID: "one", Protocol: compiled.Identity(), Node: node, Digest: protocol.ContinuationNodeDigest(node), ExpiresAt: time.Now().Add(time.Hour).Unix()}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, ending := range []string{"complete", deliveryWrite, deliveryFlush, "decode-failure"} {
		t.Run(ending, func(t *testing.T) {
			binding, err := compiled.BindContinuation(protocol.EncodeResponse, compiled.Identity(), protocol.Scope{}, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := binding.Append(sealed); err != nil {
				t.Fatal(err)
			}
			reply := httptest.NewRecorder()
			c := newTestContext(reply)
			state := trackHTTPDelivery(t.Context(), c)
			body := `{"payload":{"schemaVersion":2,"id":"r","content":[{"kind":"text","payload":"hi"}]}}`
			if ending == "decode-failure" {
				body = `{"payload":false}`
			}
			candidate := gatewayCandidate{compiled: compiled, binding: protocol.Binding{Capabilities: protocol.CapabilitySet{protocol.TextCapability: true}},
				continuation: &gatewayContinuation{isPackage: true, binding: binding}}
			server := &Server{}
			err = server.forwardGatewayResponse(t.Context(), c, &usageRecord{StartedAt: time.Now()}, &gatewayResponsePlan{ingress: compiled}, candidate,
				&http.Response{Body: io.NopCloser(strings.NewReader(body))}, protocol.ConversionContext{}, protocol.EvaluationContext{})
			if ending == "decode-failure" {
				if err == nil || binding.HasPending() || binding.Append(sealed) == nil {
					t.Fatal("early failure retained reusable carrier state", err)
				}
				return
			}
			if err != nil || !binding.HasPending() || state.result != nil {
				t.Fatal("handler return prematurely settled carrier delivery", err, state.result)
			}
			if ending != "complete" {
				state.err = wrapDownstreamError(errors.New("synthetic response delivery failure"), ending)
			}
			flushTestResponse(c)
			if binding.HasPending() || binding.Append(sealed) == nil || binding.CommitDelivery() == nil {
				t.Fatal("HTTP completion retained reusable carrier state")
			}
			if ending == "complete" {
				if state.result.Outcome != deliveryCompleted || !strings.Contains(reply.Body.String(), sealed.Token()) {
					t.Fatal("successful response lost its carrier", state.result, reply.Body.String())
				}
			} else if state.result.Outcome != deliveryFailed || state.result.Phase != ending {
				t.Fatal("failed response acquired successful delivery", state.result)
			}
		})
	}
}
