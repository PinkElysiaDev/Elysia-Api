package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

const sharedBudgetStream = `{"type":"response.started","responseId":"r","sequence":0}
{"type":"item.started","responseId":"r","sequence":1,"itemId":"text","item":{"kind":"text"}}
{"type":"item.delta","responseId":"r","sequence":2,"itemId":"text","delta":"hi"}
{"type":"item.finished","responseId":"r","sequence":3,"itemId":"text"}
{"type":"response.finished","responseId":"r","sequence":4}
`

func sharedStreamBudgetPackage(t *testing.T, items int) *protocol.Compiled {
	t.Helper()
	document := outputAwareStreamPackage(t)
	limits := protocol.DefaultLimits()
	limits.StateItems = items
	document.Mapping.Limits = &limits
	operation := document.Spec.Operations["generate"]
	operation.Transport, operation.Response = protocol.NDJSON, "provider-event"
	document.Spec.Operations["generate"] = operation
	document.Mapping.Operations["generate"] = protocol.ProtocolOperationMapping{Request: protocol.EncodeRequest, Response: protocol.DecodeEvent}
	return compileSourceStreamPackage(t, document)
}

func TestGatewayStreamSharesRetentionAcrossOwners(t *testing.T) {
	for _, capacity := range []int{2, 4} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			compiled := sharedStreamBudgetPackage(t, capacity)
			operation := compiled.Operations()["generate"]
			candidate := gatewayCandidate{compiled: compiled, operation: operation, binding: protocol.Binding{Capabilities: compiled.DeclaredCapabilities()}}
			reply := httptest.NewRecorder()
			c := newTestContext(reply)
			setTestRequest(c, httptest.NewRequest(http.MethodPost, "/generate", nil))
			record := &usageRecord{RequestID: "shared-budget", StartedAt: time.Now()}
			server := &Server{}
			err := server.forwardGatewayStream(t.Context(), c, record, &gatewayResponsePlan{ingress: compiled, operation: operation}, candidate,
				&http.Response{Body: io.NopCloser(strings.NewReader(sharedBudgetStream))})
			flushTestResponse(c)
			if capacity == 2 {
				var failure *protocol.ResourceLimitError
				if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 3 {
					t.Fatal("source and output state obtained separate budgets", err)
				}
				if strings.Contains(reply.Body.String(), "item.started") || strings.Contains(reply.Body.String(), "response.finished") {
					t.Fatal("over-budget state reached the client", reply.Body.String())
				}
				return
			}
			if err != nil || !strings.Contains(reply.Body.String(), `"delta":"hi"`) || !strings.Contains(reply.Body.String(), "response.finished") {
				t.Fatal("stream within shared capacity failed", reply.Body.String(), err)
			}
		})
	}
}

func TestGenerationCollectionSharesRetentionBeforeText(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, sharedBudgetStream)
	}))
	defer provider.Close()
	for _, capacity := range []int{2, 3} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			compiled := sharedStreamBudgetPackage(t, capacity)
			candidate := gatewayCandidate{compiled: compiled, operation: compiled.Operations()["generate"], operationName: "generate",
				model:   config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL},
				binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.DeclaredCapabilities(), Transports: []protocol.Transport{protocol.NDJSON}}}
			request := &protocol.Request{SchemaVersion: protocol.ProtocolCoreVersion, Model: protocol.StringValue("m")}
			var text strings.Builder
			before := calls.Load()
			response, err := server.collectProtocolGeneration(t.Context(), candidate, request, nil, func(_ protocol.NodeKind, delta string) { text.WriteString(delta) })
			if calls.Load() != before+1 {
				t.Fatal("collection failed before upstream or repeated generation", calls.Load()-before, err)
			}
			if capacity == 2 {
				var failure *protocol.ResourceLimitError
				if !errors.As(err, &failure) || failure.Observed != 3 || text.Len() != 0 {
					t.Fatal("over-budget collection published text", text.String(), err)
				}
				return
			}
			if err != nil || text.String() != "hi" || len(response.Content) != 1 || !response.Content[0].Payload.IsString("hi") {
				t.Fatal("bounded collection lost its returned response", response, text.String(), err)
			}
		})
	}
}
