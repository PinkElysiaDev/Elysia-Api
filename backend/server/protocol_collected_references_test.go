package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestAgentCollectionChecksResponseReferences(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	compiled := conversionPackage(t, protocol.PresetChatCompletionsID)
	var reply atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, reply.Load().(string))
	}))
	defer provider.Close()
	for _, isStream := range []bool{false, true} {
		name, transport := "generate", protocol.HTTPJSON
		if isStream {
			name, transport = "stream", protocol.SSE
		}
		operation, _ := compiled.Operation(name)
		candidate := gatewayCandidate{compiled: compiled, operation: operation, operationName: name,
			model:   config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL},
			binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.DeclaredCapabilities(), Transports: []protocol.Transport{transport}}}
		request, err := compiled.DecodeRequest(t.Context(), []byte(buildPackageGatewayRequest("openai-chat", isStream)), protocol.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		for _, end := range []int{2, 3} {
			t.Run(fmt.Sprintf("stream=%t/end=%d", isStream, end), func(t *testing.T) {
				annotation := fmt.Sprintf(`[{"type":"url_citation","url_citation":{"url":"https://example.test","title":"reference","start_index":0,"end_index":%d}}]`, end)
				body := fmt.Sprintf(`{"id":"r","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi","annotations":%s},"finish_reason":"stop"}]}`, annotation)
				if isStream {
					body = "data: " + `{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}` + "\n\n" +
						"data: " + fmt.Sprintf(`{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"annotations":%s},"finish_reason":null}]}`, annotation) + "\n\n" +
						"data: " + `{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				}
				reply.Store(body)
				before := calls.Load()
				_, err := s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
				if (err == nil) != (end == 2) || err != nil && !strings.Contains(err.Error(), "reference") || calls.Load() != before+1 {
					t.Fatal("Agent collection missed reference boundary or repeated generation", err, calls.Load()-before)
				}
			})
		}
	}
}
