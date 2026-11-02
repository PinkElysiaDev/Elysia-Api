package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// The escaped prefix and suffix prove that native compound frames replay once
// on both sides of an actual semantic edit. Snapshots are explicitly rewritten
// by the policy to agree with the delivered deltas; the encoder cannot guess.
func TestPackageGatewayNativeReconstructedNative(t *testing.T) {
	for _, name := range gatewayPackageNames {
		t.Run(name, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			compiled := seedGatewayPackages(t, server)[name]
			frames, prefix, suffix := packageNativeTransitionFrames(t, compiled, name)
			var upstream bytes.Buffer
			for _, frame := range frames {
				if err := protocol.WriteFrame(&upstream, compiled.Operations()["stream"], frame); err != nil {
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
			setupGatewayModelWithCapabilities(t, server, compiled, provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
			var delta, snapshot protocol.Expression
			if err := mustProtocolValue(t, `{"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/delta"},{"op":"literal","value":"X"}]},"then":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"literal","value":{"delta":"!"}}]},"otherwise":{"op":"read"}}`).Decode(&delta); err != nil {
				t.Fatal(err)
			}
			if err := mustProtocolValue(t, `{"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/payload"},{"op":"literal","value":"hXi"}]},"then":{"op":"merge","policy":"last","items":[{"op":"read"},{"op":"literal","value":{"payload":"h!i"}}]},"otherwise":{"op":"read"}}`).Decode(&snapshot); err != nil {
				t.Fatal(err)
			}
			policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "native-transition", Rules: []protocol.ConversionRule{
				{ID: "edit-delta", Order: 1000, Enabled: true, Phase: protocol.ConversionEvent, Action: "transform", Expression: &delta},
				{ID: "edit-snapshot", Order: 1010, Enabled: true, Phase: protocol.ConversionEvent, Match: protocol.ConversionMatch{NodeKind: protocol.TextNode}, Action: "transform", Expression: &snapshot},
			}}
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
			path := strings.ReplaceAll(compiled.Operations()["stream"].Path, "{model}", "group")
			request := httptest.NewRequest(http.MethodPost, "/gateway/"+compiled.Identity().DefinitionID+path, strings.NewReader(buildPackageGatewayRequest(name, true)))
			request.Header.Set("Authorization", "Bearer gateway-test-token")
			reply := httptest.NewRecorder()
			serveTestHTTP(server.engine, reply, request)
			body := reply.Body.String()
			if reply.Code != http.StatusOK || calls.Load() != 1 || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal("mixed delivery failed or repeated upstream", reply.Code, calls.Load(), body, reply.Result().Trailer, latestUsageRecords(t, server))
			}
			if strings.Count(body, prefix) != 1 || strings.Count(body, suffix) != 1 || strings.Index(body, prefix) >= strings.Index(body, suffix) {
				t.Fatal("native prefix or resumed suffix was rebuilt, duplicated or reordered", body)
			}
			response := collectPackageGatewayReply(t, compiled, reply, true, protocol.Scope{})
			checkPackageGatewayReply(t, response, "h!i", 20, 2)
			records := latestUsageRecords(t, server)
			if len(records) != 1 || records[0].Error != "" {
				t.Fatal("mixed stream did not settle once", records)
			}
		})
	}
}

func packageNativeTransitionFrames(t *testing.T, compiled *protocol.Compiled, name string) ([]protocol.Value, string, string) {
	t.Helper()
	document, _ := compiled.Package()
	var original []protocol.Object
	for _, sample := range document.Mapping.Samples {
		if sample.ID == "declared-stream-native-decode" {
			if err := sample.Input.Decode(&original); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	var frames []protocol.Value
	var prefix, suffix string
	appendFrame := func(raw, escaped string) string {
		if name == "openai-responses" {
			fields, err := mustProtocolValue(t, raw).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			fields["sequence_number"] = mustEncodedProtocolValue(t, len(frames))
			raw = mustEncodedProtocolValue(t, fields).JSONString()
		}
		if escaped != "" {
			raw = strings.ReplaceAll(raw, `"`+escaped+`"`, fmt.Sprintf(`"\u%04x"`, escaped[0]))
		}
		frame := mustProtocolValue(t, raw)
		frames = append(frames, frame)
		return frame.JSONString()
	}
	for index, fields := range original {
		delete(fields, "extension")
		raw := mustEncodedProtocolValue(t, fields).JSONString()
		isDelta := name == "anthropic" && fields["type"].IsString("content_block_delta") || name == "openai-responses" && fields["type"].IsString("response.output_text.delta") || (name == "openai-chat" || name == "google-generate-content") && index == 0
		if isDelta {
			prefix = appendFrame(strings.ReplaceAll(raw, `"hi"`, `"h"`), "h")
			appendFrame(strings.ReplaceAll(raw, `"hi"`, `"X"`), "")
			suffix = appendFrame(strings.ReplaceAll(raw, `"hi"`, `"i"`), "i")
		} else {
			appendFrame(strings.ReplaceAll(raw, `"hi"`, `"hXi"`), "")
		}
	}
	if prefix == "" || suffix == "" {
		t.Fatal("native transition fixture has no text delta")
	}
	return frames, prefix, suffix
}
