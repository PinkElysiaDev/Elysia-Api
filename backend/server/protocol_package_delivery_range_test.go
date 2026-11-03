package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

func TestPackageGatewayStreamDeliveryRanges(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, server)
	source, target := packages["openai-responses"], packages["openai-chat"]
	raw, err := os.ReadFile("../protocol/builtin/testdata/responses-text-ranges.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.ReplaceAll(strings.ReplaceAll(string(raw), `"hi"`, `"range-body"`), `"end_index":2`, `"end_index":10`)
	var frames []protocol.Value
	if err := mustProtocolValue(t, fixture).Decode(&frames); err != nil {
		t.Fatal(err)
	}
	// Deliver the reference before any completion snapshot, so rejection proves
	// the edited delta's association rather than snapshot prefix consistency.
	annotation := mustProtocolValue(t, `{"type":"response.output_text.annotation.added","output_index":0,"content_index":1,"annotation_index":0,"item_id":"r_item_0","annotation":{"type":"url_citation","url":"https://example.test","title":"reference","start_index":0,"end_index":10}}`)
	frames = append(frames[:8:8], append([]protocol.Value{annotation}, frames[8:]...)...)
	for index, frame := range frames {
		fields, err := frame.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		fields["sequence_number"] = mustProtocolValue(t, strconv.Itoa(index))
		frames[index] = mustEncodedProtocolValue(t, fields)
	}
	var upstream bytes.Buffer
	for _, frame := range frames {
		if err := protocol.WriteFrame(&upstream, source.Operations()["stream"], frame); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write(upstream.Bytes())
	}))
	defer provider.Close()
	setupGatewayModelWithCapabilities(t, server, source, provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
	for _, phase := range []protocol.ConversionPhase{protocol.ConversionWire, protocol.ConversionEvent} {
		for _, replacement := range []string{"range-body", "RANGE-body"} {
			var expression protocol.Expression
			if err := mustProtocolValue(t, fmt.Sprintf(`{"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/choices/0/delta/content"},{"op":"literal","value":"range-body"}]},"then":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"object","fields":{"choices":{"op":"map","source":{"op":"read","path":"/choices"},"body":{"op":"merge","policy":"last","items":[{"op":"read","from":"item"},{"op":"object","fields":{"delta":{"op":"merge","policy":"last","items":[{"op":"read","from":"item","path":"/delta"},{"op":"literal","value":{"content":%q}}]}}}]}}}}]},"otherwise":{"op":"read"}}`, replacement)).Decode(&expression); err != nil {
				t.Fatal(err)
			}
			if phase == protocol.ConversionEvent {
				if err := mustProtocolValue(t, fmt.Sprintf(`{"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/delta"},{"op":"literal","value":"range-body"}]},"then":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"literal","value":{"delta":%q}}]},"otherwise":{"op":"read"}}`, replacement)).Decode(&expression); err != nil {
					t.Fatal(err)
				}
			}

			policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "delivery-rewrite", Rules: []protocol.ConversionRule{{ID: "delivery-rewrite", Order: 1000, Enabled: true, Phase: phase, Action: "transform", Expression: &expression}}}
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
			request := httptest.NewRequest(http.MethodPost, "/gateway/"+target.Identity().DefinitionID+target.Operations()["stream"].Path, strings.NewReader(buildPackageGatewayRequest("openai-chat", true)))
			request.Header.Set("Authorization", "Bearer gateway-test-token")
			reply := httptest.NewRecorder()
			prior := calls.Load()
			serveTestHTTP(server.engine, reply, request)
			if reply.Code != http.StatusOK || calls.Load() != prior+1 || !strings.Contains(reply.Body.String(), replacement) {
				t.Fatal("incremental text was rejected or generation repeated", reply.Code, calls.Load()-prior, reply.Body.String())
			}
			if replacement == "range-body" {
				body := reply.Body.String()
				response := collectPackageGatewayReply(t, target, reply, true, protocol.Scope{})
				checkPackageGatewayReply(t, response, "😀range-body", 20, 2)
				if !strings.Contains(body, "url_citation") || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
					t.Fatal("unchanged reference or successful termination was lost", body)
				}
			} else if strings.Contains(reply.Body.String(), "url_citation") || strings.Contains(reply.Body.String(), "[DONE]") || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" {
				t.Fatal("late reference to rewritten text reached the client", reply.Body.String(), reply.Result().Trailer)
			}
		}
	}
}
