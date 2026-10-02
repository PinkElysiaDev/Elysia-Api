package relay

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
)

type socketTranslator struct{ isClosed bool }

func (translator *socketTranslator) Convert(_ context.Context, _ protocol.EventOrigin, frame protocol.SessionFrame) ([]protocol.SessionFrame, error) {
	translator.isClosed = !frame.IsBinary && string(frame.Payload) == "close"
	return []protocol.SessionFrame{frame}, nil
}
func (*socketTranslator) Finish() error             { return nil }
func (translator *socketTranslator) IsClosed() bool { return translator.isClosed }

func openSocketPair(t *testing.T, config protocol.SessionConfig) (*WebSocketSession, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *WebSocketSession, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := AcceptProtocolSession(w, r, config)
		if err == nil {
			accepted <- connection
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	peer, _, err := websocket.Dial(ctx, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.CloseNow() })
	select {
	case connection := <-accepted:
		t.Cleanup(func() { _ = connection.Close() })
		return connection, peer
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil, nil
	}
}

func TestWebSocketSchedulerBinaryHeartbeatAndCancellation(t *testing.T) {
	config := protocol.DefaultSessionConfig()
	config.CloseMillis, config.PingMillis, config.IdleMillis = 100, 200, 2000
	client, clientPeer := openSocketPair(t, config)
	upstream, upstreamPeer := openSocketPair(t, config)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- protocol.RunSession(ctx, client, upstream, config, config, &socketTranslator{}) }()
	payload := []byte{0, 255, 128, 13, 10, 0, 42}
	for _, pair := range [][2]*websocket.Conn{{clientPeer, upstreamPeer}, {upstreamPeer, clientPeer}} {
		if err := pair[0].Write(t.Context(), websocket.MessageBinary, payload); err != nil {
			t.Fatal(err)
		}
		kind, received, err := pair[1].Read(t.Context())
		if err != nil || kind != websocket.MessageBinary || !bytes.Equal(received, payload) {
			t.Fatalf("binary changed: %v %x", err, received)
		}
	}
	// Peer readers process pings while the shared coordinator remains idle.
	readDone := make(chan error, 2)
	for _, peer := range []*websocket.Conn{clientPeer, upstreamPeer} {
		go func() { _, _, err := peer.Read(ctx); readDone <- err }()
	}
	for _, connection := range []*WebSocketSession{client, upstream} {
		deadline, stop := context.WithTimeout(ctx, time.Second)
		err := connection.Ping(deadline)
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("socket workers did not exit")
	}
	for range 2 {
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Fatal("peer read leaked")
		}
	}
}

func TestWebSocketSchedulerBoundsUnresponsiveClose(t *testing.T) {
	config := protocol.DefaultSessionConfig()
	config.CloseMillis = 50
	client, peer := openSocketPair(t, config)
	upstream, _ := openSocketPair(t, config)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- protocol.RunSession(ctx, client, upstream, config, config, &socketTranslator{}) }()
	if err := peer.Write(ctx, websocket.MessageText, []byte("close")); err != nil {
		t.Fatal(err)
	}
	// Neither peer reads/acknowledges the closing handshake.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close exceeded configured deadline")
	}
}

func TestWebSocketSchedulerCancelsBlockedNetworkWriter(t *testing.T) {
	config := protocol.DefaultSessionConfig()
	config.QueueItems, config.QueueBytes = 1, config.FrameBytes
	client, _ := openSocketPair(t, config)
	upstream, peer := openSocketPair(t, config)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done, senderDone := make(chan error, 1), make(chan error, 1)
	go func() { done <- protocol.RunSession(ctx, client, upstream, config, config, &socketTranslator{}) }()
	started := make(chan struct{})
	go func() {
		payload := bytes.Repeat([]byte{42}, config.FrameBytes)
		close(started)
		for range 64 {
			if err := peer.Write(ctx, websocket.MessageBinary, payload); err != nil {
				senderDone <- err
				return
			}
		}
		senderDone <- nil
	}()
	<-started
	// A client that never reads eventually fills the socket and one-frame queue.
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked network writer leaked")
	}
	select {
	case <-senderDone:
	case <-time.After(time.Second):
		t.Fatal("sender leaked")
	}
}

func TestWebSocketDialRefusesCredentialRedirect(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("credential redirect followed")
		w.WriteHeader(500)
	}))
	defer redirect.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect.URL, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()
	adapter := NewOpenAIAdapter(time.Second)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, provider.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer local-test-only")
	if _, err := adapter.DialProtocolSession(t.Context(), request, protocol.DefaultSessionConfig()); err == nil {
		t.Fatal("redirect accepted")
	}
}
