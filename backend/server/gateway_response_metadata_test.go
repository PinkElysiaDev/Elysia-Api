package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

const gatewayResponsesMetadata = `{"instructions":null,"previous_response_id":null,"tool_choice":"auto"}`
const gatewayGeminiMetadata = `{"createTime":"2026-11-14T00:00:00Z"}`
const gatewayMetadataLogTimeout = 3 * time.Second
const gatewayMetadataLogInterval = 20 * time.Millisecond

func TestGatewayDeclaredResponseMetadata(t *testing.T) {
	for _, fixture := range []struct{ name, metadata string }{
		{"openai-responses", gatewayResponsesMetadata},
		{"google-generate-content", gatewayGeminiMetadata},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			packages := defaultGatewayPackages(t, server)
			upstream := packages[fixture.name]
			metadata, err := mustProtocolValue(t, fixture.metadata).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			provider, calls := newGatewayMetadataProvider(t, upstream, metadata)
			setupGatewayModelWithCapabilities(t, server, upstream, provider.URL, protocol.CapabilitySet{
				protocol.TextCapability: true, protocol.UsageCapability: true,
			})
			for _, ingressName := range gatewayPackageNames {
				for _, isStream := range []bool{false, true} {
					operation := "generate"
					if isStream {
						operation = "stream"
					}
					t.Run(ingressName+"/"+operation, func(t *testing.T) {
						ingress := packages[ingressName]
						path := strings.ReplaceAll(ingress.Operations()[operation].Path, "{model}", "group")
						body := buildPackageGatewayRequest(ingressName, isStream)
						request := httptest.NewRequest(http.MethodPost, "/gateway/"+ingress.Identity().DefinitionID+path, strings.NewReader(body))
						request.Header.Set("Authorization", "Bearer gateway-test-token")
						request.Header.Set("Content-Type", "application/json")
						before := calls.Load()
						reply := httptest.NewRecorder()
						serveTestHTTP(server.engine, reply, request)
						if reply.Code != http.StatusOK || calls.Load() != before+1 || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
							t.Fatal("declared response metadata prevented delivery", reply.Code, calls.Load()-before, reply.Body.String())
						}
						response := collectPackageGatewayReply(t, ingress, reply, isStream, protocol.Scope{})
						checkPackageGatewayReply(t, response, "hi", 20, 2)
						if ingressName == fixture.name {
							checkGatewayResponseMetadata(t, response, metadata)
						} else {
							checkGatewayMetadataProjection(t, server, reply.Header().Get("X-Elysia-Request-Id"), metadata)
						}
					})
				}
			}
		})
	}
}

func newGatewayMetadataProvider(t *testing.T, upstream *protocol.Compiled, metadata protocol.Object) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	jsonReply := buildGatewayMetadataReply(t, upstream, metadata, false)
	streamReply := buildGatewayMetadataReply(t, upstream, metadata, true)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var control struct {
			IsStream bool `json:"stream"`
		}
		if err := sonic.ConfigStd.Unmarshal(body, &control); err != nil {
			t.Error(err)
			return
		}
		if control.IsStream || request.URL.Query().Get("alt") == "sse" {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write(streamReply)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonReply)
	}))
	t.Cleanup(provider.Close)
	return provider, &calls
}

func buildGatewayMetadataReply(t *testing.T, upstream *protocol.Compiled, metadata protocol.Object, isStream bool) []byte {
	t.Helper()
	annotate := func(value protocol.Value) protocol.Value {
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		owner := fields
		isEnvelope := isStream && upstream.Identity().DefinitionID == protocol.PresetResponsesID
		if isEnvelope {
			if fields["response"].IsZero() {
				return value
			}
			owner, err = fields["response"].ReadObject()
			if err != nil {
				t.Fatal(err)
			}
		}
		for name, value := range metadata {
			owner[name] = value
		}
		if isEnvelope {
			fields["response"] = mustEncodedProtocolValue(t, owner)
		}
		return mustEncodedProtocolValue(t, fields)
	}
	body := buildPackageGatewayReply(t, upstream, isStream)
	if !isStream {
		return annotate(mustProtocolValue(t, string(body))).Bytes()
	}
	var output bytes.Buffer
	operation := upstream.Operations()["stream"]
	err := protocol.ReadFrames(t.Context(), bytes.NewReader(body), operation, upstream.ResourceLimits().BufferBytes, func(value protocol.Value, _ protocol.Object) error {
		return protocol.WriteFrame(&output, operation, annotate(value))
	})
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func checkGatewayResponseMetadata(t *testing.T, response *protocol.Response, expected protocol.Object) {
	t.Helper()
	for name, value := range expected {
		matches := 0
		for _, field := range response.Metadata {
			if field.Location == "response" && field.Name == name {
				matches++
				if field.Value.JSONString() != value.JSONString() || field.Path != "/"+name {
					t.Fatal("same-source metadata value or path changed", field)
				}
			}
		}
		if matches != 1 {
			t.Fatal("same-source metadata was lost or duplicated", name, matches)
		}
	}
}

func readGatewayUsageRecord(t *testing.T, server *Server, requestID string) usageRecord {
	t.Helper()
	if requestID == "" {
		t.Fatal("gateway response omitted its request ID")
	}
	var record usageRecord
	deadline := time.Now().Add(gatewayMetadataLogTimeout)
	for {
		raw, isFound, err := server.store.GetUsageRecordJSON(t.Context(), requestID)
		if err != nil {
			t.Fatal(err)
		}
		if isFound {
			if err := sonic.ConfigStd.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			return record
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway request did not persist its diagnostics", requestID)
		}
		time.Sleep(gatewayMetadataLogInterval)
	}
}

func checkGatewayMetadataProjection(t *testing.T, server *Server, requestID string, expected protocol.Object) {
	t.Helper()
	record := readGatewayUsageRecord(t, server, requestID)
	for name, value := range expected {
		fidelity := "lossy_compatible"
		if value.IsNull() {
			fidelity = "preserved"
		}
		hasDiagnostic := false
		for _, issue := range record.ConversionIssues {
			if strings.HasSuffix(issue.Path, "/"+name) && issue.RuleID != "" && issue.PolicyHash == record.ConversionPolicyHash && issue.Fidelity == fidelity {
				hasDiagnostic = true
				break
			}
		}
		if !hasDiagnostic {
			t.Fatal("metadata projection lacks its policy diagnostic", name, record.ConversionIssues)
		}
	}
}
