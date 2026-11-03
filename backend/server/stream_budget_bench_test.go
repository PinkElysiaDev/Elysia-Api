package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// This measures the production forwarding pipeline with in-memory downstream
// transport. The same successful workload can run against the pre-binding code.
func BenchmarkPackageStreamBudget(b *testing.B) {
	const deltaBytes = 256
	for _, isNative := range []bool{true, false} {
		mode := "mapped"
		if isNative {
			mode = "native"
		}
		for _, frames := range []int{128, 2048} {
			b.Run(fmt.Sprintf("%s/%d", mode, frames), func(b *testing.B) {
				document := outputAwareStreamPackage(b)
				document.Mapping.Native.Preserve = isNative
				compiled := compileSourceStreamPackage(b, document)
				operation := protocol.Operation{Kind: "generate", Transport: protocol.NDJSON}
				candidate := gatewayCandidate{compiled: compiled, operation: operation, binding: protocol.Binding{Capabilities: compiled.DeclaredCapabilities()}}
				plan := &gatewayResponsePlan{ingress: compiled, operation: operation}
				var source strings.Builder
				source.WriteString("{\"type\":\"response.started\",\"responseId\":\"r\",\"sequence\":0}\n")
				source.WriteString("{\"type\":\"item.started\",\"responseId\":\"r\",\"sequence\":1,\"itemId\":\"text\",\"item\":{\"kind\":\"text\"}}\n")
				for index := range frames {
					fmt.Fprintf(&source, "{\"type\":\"item.delta\",\"responseId\":\"r\",\"sequence\":%d,\"itemId\":\"text\",\"delta\":%q}\n", index+2, strings.Repeat("x", deltaBytes))
				}
				fmt.Fprintf(&source, "{\"type\":\"item.finished\",\"responseId\":\"r\",\"sequence\":%d,\"itemId\":\"text\"}\n", frames+2)
				fmt.Fprintf(&source, "{\"type\":\"response.finished\",\"responseId\":\"r\",\"sequence\":%d}\n", frames+3)
				body := source.String()
				server := &Server{}
				b.ReportAllocs()
				b.SetBytes(int64(frames * deltaBytes))
				for b.Loop() {
					reply := httptest.NewRecorder()
					c := newTestContext(reply)
					setTestRequest(c, httptest.NewRequest(http.MethodPost, "/generate", nil))
					record := &usageRecord{RequestID: "stream-benchmark", StartedAt: time.Now()}
					err := server.forwardGatewayStream(b.Context(), c, record, plan, candidate, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
					flushTestResponse(c)
					if err != nil || strings.Count(reply.Body.String(), `"type":"item.delta"`) != frames || !strings.Contains(reply.Body.String(), `"type":"response.finished"`) {
						b.Fatal("incomplete stream", err)
					}
				}
			})
		}
	}
}
