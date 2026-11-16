package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

const gatewayExtensionBodyKB = 128

func TestGatewaySavedResponseExtensions(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	bodyLimit, isErrorOnly := gatewayExtensionBodyKB, false
	server.config.SetUsageLogConfig(config.UsageLogConfig{BodyMaxKB: &bodyLimit, BodyOnErrorOnly: &isErrorOnly})
	packages := defaultGatewayPackages(t, server)
	readFixture := func(suffix string) []byte {
		raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "testdata", "responses-live-extensions."+suffix))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	jsonReply, streamReply := readFixture("json"), readFixture("sse")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var control struct {
			IsStream bool `json:"stream"`
		}
		if err := sonic.ConfigStd.Unmarshal(body, &control); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if control.IsStream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write(streamReply)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonReply)
	}))
	t.Cleanup(provider.Close)
	setupGatewayModelWithCapabilities(t, server, packages["openai-responses"], provider.URL, protocol.CapabilitySet{
		protocol.TextCapability: true, protocol.UsageCapability: true,
	})
	for _, sourceName := range gatewayPackageNames {
		for _, isStream := range []bool{false, true} {
			operation := "generate"
			if isStream {
				operation = "stream"
			}
			t.Run(sourceName+"/"+operation, func(t *testing.T) {
				source := packages[sourceName]
				path := strings.ReplaceAll(source.Operations()[operation].Path, "{model}", "group")
				body := buildPackageGatewayRequest(sourceName, isStream)
				request := httptest.NewRequest(http.MethodPost, "/gateway/"+source.Identity().DefinitionID+path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gateway-test-token")
				request.Header.Set("Content-Type", "application/json")
				before := calls.Load()
				reply := httptest.NewRecorder()
				serveTestHTTP(server.engine, reply, request)
				if reply.Code != http.StatusOK || calls.Load() != before+1 || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
					t.Fatal("full saved response extensions failed delivery", reply.Code, calls.Load()-before, reply.Body.String())
				}
				response := collectPackageGatewayReply(t, source, reply, isStream, protocol.Scope{})
				checkPackageGatewayReply(t, response, "OK", 19, 5)
				requestID := reply.Header().Get("X-Elysia-Request-Id")
				record := readGatewayUsageRecord(t, server, requestID)
				if record.RequestID != requestID || !reflect.DeepEqual(decodeGatewayJSON(t, []byte(record.IncomingBody.Content)), decodeGatewayJSON(t, []byte(body))) || record.IncomingBody.Truncated || record.OutgoingBody.Content == "" || record.OutgoingBody.Truncated {
					t.Fatal("complete request bodies were not retained", record.IncomingBody, record.OutgoingBody)
				}
				upstream := jsonReply
				if isStream {
					upstream = streamReply
				}
				if record.ProviderResponse.Truncated {
					t.Fatal("saved provider body was truncated", record.ProviderResponse)
				}
				checkGatewaySavedBody(t, packages["openai-responses"], []byte(record.ProviderResponse.Content), upstream, isStream)
			})
		}
	}
}

func decodeGatewayJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func checkGatewaySavedBody(t *testing.T, compiled *protocol.Compiled, actual, expected []byte, isStream bool) {
	t.Helper()
	if !isStream {
		if !reflect.DeepEqual(decodeGatewayJSON(t, actual), decodeGatewayJSON(t, expected)) {
			t.Fatal("saved provider JSON changed members or exact numbers")
		}
		return
	}
	var frames []any
	err := protocol.ReadFrames(t.Context(), bytes.NewReader(expected), compiled.Operations()["stream"], compiled.ResourceLimits().BufferBytes, func(value protocol.Value, _ protocol.Object) error {
		frames = append(frames, decodeGatewayJSON(t, value.Bytes()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The usage log retains ordered event payloads; the fixture retains raw SSE framing.
	if !reflect.DeepEqual(decodeGatewayJSON(t, actual), frames) {
		t.Fatal("saved provider stream changed frames, fields or exact numbers")
	}
}
