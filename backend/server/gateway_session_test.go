package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func setupSessionModel(t *testing.T, server *Server, compiled *protocol.Compiled, url string) {
	t.Helper()
	setupGatewayModel(t, server, compiled, url)
	canTools := true
	if _, err := server.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &canTools}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.gatewaySessions.stop)
	server.invalidateRouteCache()
}

func readSessionEvents(t *testing.T, ctx context.Context, connection *websocket.Conn, compiled *protocol.Compiled, isClient bool) []protocol.Event {
	t.Helper()
	_, body, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	value, err := protocol.ParseValue(body)
	if err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	if isClient {
		events, err = compiled.DecodeClientEvents(ctx, value, protocol.EvaluationContext{})
	} else {
		events, err = compiled.DecodeEvents(ctx, value, protocol.EvaluationContext{})
	}
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestGatewayWebSocketIndependentProtocolsAndUsage(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	ingress := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "session-alpha"))
	definition := loadGatewayDefinition(t, "session-beta")
	upstream := activateGatewayDefinition(t, server, definition)
	providerDone := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("deployment") != "upstream-model" || request.URL.Query().Get("key") != "" || request.Header.Get("Authorization") != "" {
			providerDone <- fmt.Errorf("gateway credentials/model leaked into handshake")
			writer.WriteHeader(400)
			return
		}
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			providerDone <- err
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, step := range definition.SessionSamples[0].Steps {
			if step.Direction == protocol.DecodeEvent {
				if err := connection.Write(ctx, websocket.MessageText, step.Input.Bytes()); err != nil {
					providerDone <- err
					return
				}
				continue
			}
			_, body, err := connection.Read(ctx)
			if err != nil {
				providerDone <- err
				return
			}
			value, err := protocol.ParseValue(body)
			if err != nil {
				providerDone <- err
				return
			}
			events, err := upstream.DecodeClientEvents(ctx, value, protocol.EvaluationContext{})
			if err != nil {
				providerDone <- err
				return
			}
			if len(events) != 1 {
				providerDone <- fmt.Errorf("missing client event")
				return
			}
			if events[0].Request != nil && string(events[0].Request.Model.Bytes()) != `"upstream-model"` {
				providerDone <- fmt.Errorf("session model was not mapped")
				return
			}
		}
		providerDone <- connection.Close(websocket.StatusNormalClosure, "")
	}))
	defer provider.Close()
	setupSessionModel(t, server, upstream, provider.URL)
	gateway := httptest.NewServer(server.engine)
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, gateway.URL+"/gateway/session-alpha/session?model=group&key=gateway-test-token", nil)
	if err != nil {
		t.Fatalf("gateway dial: %v %+v", err, response)
	}
	defer connection.CloseNow()
	clientDefinition := ingress.Definition()
	for _, step := range clientDefinition.SessionSamples[0].Steps {
		if step.Direction == protocol.DecodeEvent {
			events := readSessionEvents(t, ctx, connection, ingress, false)
			var expected []protocol.Event
			if err := step.Expected.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Type != expected[0].Type {
				t.Fatalf("unexpected event: %+v expected %+v", events, expected)
			}
			if events[0].Type == protocol.UsageUpdated && (events[0].Usage.CacheRead.Count != 40 || events[0].Usage.CacheCreation == nil || events[0].Usage.CacheCreation.Count != 0) {
				t.Fatalf("usage tail changed: %+v", events[0].Usage)
			}
			continue
		}
		wire := step.Input
		if string(step.Input.Bytes()) != "" {
			events, err := ingress.DecodeClientEvents(ctx, wire, protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if events[0].Request != nil {
				events[0].Request.Model = protocol.StringValue("group")
				wire, err = ingress.EncodeUpstreamEvent(ctx, events[0], protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := connection.Write(ctx, websocket.MessageText, wire.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close: %v", err)
	}
	if err := <-providerDone; err != nil {
		t.Fatal(err)
	}
	server.gatewaySessions.stop()
	_, logs, err := server.store.QueryUsageLogs(t.Context(), storage.UsageQuery{Limit: 10})
	if err != nil || len(logs) != 2 {
		t.Fatalf("response settlements: %v %+v", err, logs)
	}
	for _, log := range logs {
		body, _, err := server.store.GetUsageRecordJSON(t.Context(), log.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		var record usageRecord
		if err := json.Unmarshal(body, &record); err != nil {
			t.Fatal(err)
		}
		if record.IngressRevision != ingress.Hash() || record.UpstreamRevision != upstream.Hash() || record.StatusCode != 200 {
			t.Fatalf("settlement identity: %s", body)
		}
		if record.ProtocolResponseID == "r" && record.ProtocolUsage != nil {
			t.Fatalf("missing usage became zero: %s", body)
		}
		if record.ProtocolResponseID == "r2" && (record.ProtocolUsage == nil || record.ProtocolUsage.CacheRead.Count != 40 || record.UsageDetail.CacheCreationInputTokens == nil) {
			t.Fatalf("tail not persisted: %s", body)
		}
	}
}

func TestGatewayWebSocketRejectsAuthAndOriginBeforeUpstream(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	compiled := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "session-alpha"))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	setupSessionModel(t, server, compiled, provider.URL)
	gateway := httptest.NewServer(server.engine)
	defer gateway.Close()
	for _, test := range []struct {
		query, token, origin string
		status               int
	}{{"group", "bad", "", 401}, {"forbidden", "gateway-test-token", "", 403}, {"group", "gateway-test-token", "https://untrusted.example", 403}} {
		options := &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + test.token}}}
		if test.origin != "" {
			options.HTTPHeader.Set("Origin", test.origin)
		}
		_, response, err := websocket.Dial(t.Context(), gateway.URL+"/gateway/session-alpha/session?model="+test.query, options)
		if err == nil || response == nil || response.StatusCode != test.status {
			t.Fatalf("expected %d: %v %+v", test.status, err, response)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected handshake reached upstream")
	}
}

func TestGatewayWebSocketLateErrorClosesWithoutReplay(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	compiled := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "session-alpha"))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"event":"session.started","session":"s"}`))
		_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"event":"vendor.unmapped"}`))
		_, _, _ = connection.Read(r.Context())
	}))
	defer provider.Close()
	setupSessionModel(t, server, compiled, provider.URL)
	gateway := httptest.NewServer(server.engine)
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, gateway.URL+"/gateway/session-alpha/session?model=group&key=gateway-test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	events := readSessionEvents(t, ctx, connection, compiled, false)
	if events[0].Type != protocol.SessionStarted {
		t.Fatalf("first: %+v", events)
	}
	events = readSessionEvents(t, ctx, connection, compiled, false)
	if events[0].Type != protocol.OperationFailed || !strings.Contains(string(events[0].Error.Bytes()), "protocol_session_error") {
		t.Fatalf("late error: %+v", events)
	}
	_, _, err = connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Fatalf("expected error close: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("generation replayed: %d", calls.Load())
	}
}
