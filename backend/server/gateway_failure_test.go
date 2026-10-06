package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

func declareResponseFailures(t *testing.T, definition protocol.Definition, field string) protocol.Definition {
	t.Helper()
	for _, direction := range []protocol.Direction{protocol.DecodeResponse, protocol.EncodeResponse} {
		mapping := definition.Directions[direction]
		path, output := "/"+field, "error"
		if direction == protocol.EncodeResponse {
			path, output = "/error", field
		}
		fields := map[string]protocol.Expression{output: {Op: "read", Path: path, Required: true}}
		if direction == protocol.DecodeResponse {
			fields["schemaVersion"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `1`)}
			fields["content"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `[]`)}
		}
		mapping.Transform = &protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", Path: path}}, Then: &protocol.Expression{Op: "object", Fields: fields}, Otherwise: mapping.Transform}
		definition.Directions[direction] = mapping
	}
	payload := mustProtocolValue(t, `{"message":"quota exhausted","code":"exhausted"}`)
	wire := mustEncodedProtocolValue(t, protocol.Object{field: payload})
	semantic := mustEncodedProtocolValue(t, protocol.Object{"schemaVersion": mustProtocolValue(t, `1`), "content": mustProtocolValue(t, `[]`), "error": payload})
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "failure.decode", Direction: protocol.DecodeResponse, Input: wire, Expected: semantic},
		protocol.Sample{ID: "failure.encode", Direction: protocol.EncodeResponse, Input: semantic, Expected: wire})
	return definition
}

func TestCustomHTTPFailuresUseDeclaredResponseDirections(t *testing.T) {
	upstreamDefinition := declareResponseFailures(t, standaloneWireDefinition(t, protocol.HTTPJSON), "problem")
	ingress := declareResponseFailures(t, standaloneWireDefinition(t, protocol.HTTPJSON), "failure")
	ingress.ID, ingress.Family = "failure-client", "failure-client"
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write([]byte(`{"problem":{"message":"quota exhausted","code":"exhausted"}}`))
			}))
			defer upstream.Close()
			groups := standaloneGroup(t, upstreamDefinition, upstream.URL)
			groups[0].MaxRetries = 2
			second := groups[0].Models[0]
			second.ID, second.Name = second.ID+"-second", second.Name+"-second"
			groups[0].Models = append(groups[0].Models, second)
			server := newTestServer(t, groups, upstreamDefinition, ingress)
			recorder := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(recorder)
			request.Request = httptest.NewRequest(http.MethodPost, "/gateway/failure-client/v2/generate/grp", strings.NewReader(`{"deployment":"grp","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`))
			request.Params = gin.Params{{Key: "protocolId", Value: ingress.ID}, {Key: "path", Value: "/v2/generate/grp"}}
			server.gatewayProtocol(request)
			if calls.Load() != 1 {
				t.Fatalf("generation replayed %d times after a nonretryable outcome", calls.Load())
			}
			wantStatus := status
			if status == http.StatusOK {
				wantStatus = http.StatusBadGateway
			}
			var decoded map[string]map[string]string
			decodeErr := json.Unmarshal(recorder.Body.Bytes(), &decoded)
			if recorder.Code != wantStatus || decodeErr != nil || len(decoded) != 1 || len(decoded["failure"]) != 2 || decoded["failure"]["code"] != "exhausted" || decoded["failure"]["message"] != "quota exhausted" {
				t.Fatalf("declared failure was not converted: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
