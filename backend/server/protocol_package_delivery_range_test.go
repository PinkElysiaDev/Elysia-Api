package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestPackageGatewayResponseDeliveryRanges(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	compiled := seedGatewayPackages(t, server)["openai-chat"]
	var calls, start atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"id":"r","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"😀abCDxy","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.test","title":"reference","start_index":%d,"end_index":%d}}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22}}`, start.Load(), start.Load()+2)
	}))
	defer provider.Close()
	setupGatewayModelWithCapabilities(t, server, compiled, provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
	var expression protocol.Expression
	err := mustProtocolValue(t, `{"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/choices/0/message/content"},{"op":"literal","value":"😀abCDxy"}]},"then":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"object","fields":{"choices":{"op":"map","source":{"op":"read","path":"/choices"},"body":{"op":"merge","policy":"last","items":[{"op":"read","from":"item"},{"op":"object","fields":{"message":{"op":"merge","policy":"last","items":[{"op":"read","from":"item","path":"/message"},{"op":"literal","value":{"content":"😀abQxy"}}]}}}]}}}}]},"otherwise":{"op":"read"}}`).Decode(&expression)
	if err != nil {
		t.Fatal(err)
	}
	policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "delivery-rewrite", Rules: []protocol.ConversionRule{{ID: "delivery-rewrite", Order: 1000, Enabled: true, Phase: protocol.ConversionWire, Action: "transform", Expression: &expression}}}
	bindings, err := server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &policy}
	service, _ := server.protocolService()
	bindings, err = server.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
		t.Fatal(err)
	}
	server.invalidateRouteCache()
	for _, intersectsRewrite := range []bool{false, true} {
		if intersectsRewrite {
			start.Store(4)
		}
		request := httptest.NewRequest(http.MethodPost, "/gateway/"+compiled.Identity().DefinitionID+compiled.Operations()["generate"].Path, strings.NewReader(buildPackageGatewayRequest("openai-chat", false)))
		request.Header.Set("Authorization", "Bearer gateway-test-token")
		reply := httptest.NewRecorder()
		prior := calls.Load()
		serveTestHTTP(server.engine, reply, request)
		if calls.Load() != prior+1 {
			t.Fatal("final validation repeated generation", calls.Load(), reply.Body.String())
		}
		if intersectsRewrite {
			if reply.Code == http.StatusOK || !strings.Contains(reply.Body.String(), "unproven text") {
				t.Fatal("rewritten citation was delivered", reply.Code, reply.Body.String())
			}
			continue
		}
		if reply.Code != http.StatusOK {
			t.Fatal(reply.Code, reply.Body.String())
		}
		response := collectPackageGatewayReply(t, compiled, reply, false, protocol.Scope{})
		checkPackageGatewayReply(t, response, "😀abQxy", 20, 2)
		if !strings.Contains(reply.Body.String(), `"url_citation"`) {
			t.Fatal("reference disappeared at delivery")
		}
	}
}
