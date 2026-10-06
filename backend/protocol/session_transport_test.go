package protocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type memorySessionConnection struct {
	input  chan SessionFrame
	output chan SessionFrame
	closed atomic.Bool
	active atomic.Int32
}

func (connection *memorySessionConnection) Read(ctx context.Context) (SessionFrame, error) {
	connection.active.Add(1)
	defer connection.active.Add(-1)
	select {
	case <-ctx.Done():
		return SessionFrame{}, ctx.Err()
	case frame, ok := <-connection.input:
		if !ok {
			return SessionFrame{}, io.EOF
		}
		return frame, nil
	}
}
func (connection *memorySessionConnection) Write(ctx context.Context, frame SessionFrame) error {
	connection.active.Add(1)
	defer connection.active.Add(-1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case connection.output <- frame:
		return nil
	}
}
func (connection *memorySessionConnection) Ping(context.Context) error { return nil }
func (connection *memorySessionConnection) Close() error               { connection.closed.Store(true); return nil }

type copySessionTranslator struct{ finishError error }

func (*copySessionTranslator) Convert(_ context.Context, _ EventOrigin, frame SessionFrame) ([]SessionFrame, error) {
	return []SessionFrame{frame}, nil
}
func (translator *copySessionTranslator) Finish() error { return translator.finishError }
func (*copySessionTranslator) IsClosed() bool           { return false }

func memorySessionPair() (*memorySessionConnection, *memorySessionConnection) {
	return &memorySessionConnection{input: make(chan SessionFrame, 16), output: make(chan SessionFrame, 16)}, &memorySessionConnection{input: make(chan SessionFrame, 16), output: make(chan SessionFrame, 16)}
}

func TestRunSessionDrainsUsageAndBinaryBeforeEOF(t *testing.T) {
	client, upstream := memorySessionPair()
	frames := []SessionFrame{{Payload: []byte(`{"event":"response.finished"}`)}, {Payload: []byte(`{"event":"usage.updated","cacheRead":40}`)}, {IsBinary: true, Payload: []byte{0, 255, 128, 13, 10}}}
	for _, frame := range frames {
		upstream.input <- frame
	}
	close(upstream.input)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := RunSession(ctx, client, upstream, DefaultSessionConfig(), DefaultSessionConfig(), &copySessionTranslator{}); err != nil {
		t.Fatal(err)
	}
	if len(client.output) != len(frames) {
		t.Fatalf("EOF dropped queued frames: %d", len(client.output))
	}
	for _, expected := range frames {
		actual := <-client.output
		if actual.IsBinary != expected.IsBinary || !bytes.Equal(actual.Payload, expected.Payload) {
			t.Fatalf("frame changed: %+v", actual)
		}
	}
	for _, connection := range []*memorySessionConnection{client, upstream} {
		if !connection.closed.Load() || connection.active.Load() != 0 {
			t.Fatal("connection worker leaked")
		}
	}
}

func TestRunSessionCancellationUnblocksSlowClient(t *testing.T) {
	client, upstream := memorySessionPair()
	client.output = make(chan SessionFrame)
	config := DefaultSessionConfig()
	config.QueueItems, config.QueueBytes, config.FrameBytes = 1, 4, 4
	for range 10 {
		upstream.input <- SessionFrame{Payload: []byte("1234")}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := RunSession(ctx, client, upstream, config, config, &copySessionTranslator{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow client did not cancel: %v", err)
	}
	for _, connection := range []*memorySessionConnection{client, upstream} {
		if !connection.closed.Load() || connection.active.Load() != 0 {
			t.Fatal("blocked worker leaked")
		}
	}
}

func TestRunSessionRejectsOversizedFrameAndTruncatedStream(t *testing.T) {
	for _, mode := range []string{"oversize", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			client, upstream := memorySessionPair()
			config := DefaultSessionConfig()
			translator := &copySessionTranslator{}
			if mode == "oversize" {
				config.FrameBytes = 1
				upstream.input <- SessionFrame{Payload: []byte("too large")}
			} else {
				translator.finishError = streamIssue(UpstreamContractViolation, "/session", "response was incomplete")
				close(upstream.input)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := RunSession(ctx, client, upstream, config, config, translator)
			code := LimitExceeded
			if mode == "truncated" {
				code = UpstreamContractViolation
			}
			if !hasIssueCode(err, code) {
				t.Fatalf("want %s: %v", code, err)
			}
		})
	}
}

func TestSessionQueueCountsWritesInProgress(t *testing.T) {
	queue := newSessionQueue(2, 4)
	packet := sessionPacket{frame: SessionFrame{Payload: []byte("1234")}}
	if err := queue.put(t.Context(), packet); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.take(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := queue.put(ctx, packet); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write in progress escaped byte budget: %v", err)
	}
	queue.release(packet)
	if err := queue.put(t.Context(), packet); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAdapterSharesTraceRulesAndUsage(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	compiled := compileSessionDefinition(t, definition)
	binding := Binding{ProtocolID: definition.ID, RevisionHash: compiled.Hash(), Capabilities: definition.Capabilities, Transports: []Transport{WebSocket}}
	operation := compiled.Operations()["session"]
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, Scope{Model: "m"}, StringValue("m"))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range definition.SessionSamples[0].Steps {
		frames, err := adapter.Convert(t.Context(), eventOrigin(step.Direction), SessionFrame{Payload: step.Input.Bytes()})
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame count: %d", len(frames))
		}
		wire, err := ParseValue(frames[0].Payload)
		if err != nil || !equalValues(wire, step.Input) {
			t.Fatalf("wire changed: %s vs %s: %v", wire.Bytes(), step.Input.Bytes(), err)
		}
	}
	if err := adapter.Finish(); err != nil {
		t.Fatal(err)
	}
	usage := adapter.ResponseUsage()
	if usage["r"] != nil || usage["r2"].CacheRead.Count != 40 || usage["r2"].CacheCreation.Count != 0 {
		t.Fatalf("usage changed: %+v", usage)
	}
}

func TestRunSessionInterleavesDirectionsAndFlushesExplicitClose(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	compiled := compileSessionDefinition(t, definition)
	operation := compiled.Operations()["session"]
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, Binding{Capabilities: definition.Capabilities}, Scope{Model: "m"}, StringValue("m"))
	if err != nil {
		t.Fatal(err)
	}
	client, upstream := memorySessionPair()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunSession(ctx, client, upstream, *operation.Session, *operation.Session, adapter) }()
	for _, step := range definition.SessionSamples[0].Steps {
		input, output := upstream.input, client.output
		if eventOrigin(step.Direction) == ClientEvent {
			input, output = client.input, upstream.output
		}
		input <- SessionFrame{Payload: step.Input.Bytes()}
		select {
		case <-ctx.Done():
			t.Fatal("session failed to forward before deadline")
		case frame := <-output:
			value, err := ParseValue(frame.Payload)
			if err != nil || !equalValues(value, step.Input) {
				t.Fatalf("duplex frame changed: %s", frame.Payload)
			}
		}
	}
	select {
	case <-ctx.Done():
		t.Fatal("explicit close did not end the connection")
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	}
	if !client.closed.Load() || !upstream.closed.Load() {
		t.Fatal("session sockets left open")
	}
}

func TestSessionAdapterPreservesDeclaredMediaAndRejectsModelChange(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	definition.Capabilities[RealtimeMediaCapability] = true
	for _, direction := range []Direction{DecodeEvent, EncodeEvent, DecodeClientEvent, EncodeUpstreamEvent} {
		definition.Directions[direction].Capabilities[RealtimeMediaCapability] = true
	}
	definition.Operations["session"].Session.InputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}
	definition.Operations["session"].Session.OutputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}
	compiled := compileSessionDefinition(t, definition)
	operation := compiled.Operations()["session"]
	binding := Binding{Capabilities: definition.Capabilities}
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, Scope{Model: "upstream-model"}, StringValue("group"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Convert(t.Context(), UpstreamEvent, SessionFrame{Payload: []byte(`{"event":"session.started","session":"s"}`)}); err != nil {
		t.Fatal(err)
	}
	frame := SessionFrame{IsBinary: true, Payload: []byte{0, 128, 255, 13, 10}}
	for _, origin := range []EventOrigin{ClientEvent, UpstreamEvent} {
		frames, err := adapter.Convert(t.Context(), origin, frame)
		if err != nil || len(frames) != 1 || !bytes.Equal(frames[0].Payload, frame.Payload) {
			t.Fatalf("binary altered: %v %v", frames, err)
		}
	}
	configure := SessionFrame{Payload: []byte(`{"event":"session.configure","settings":{"schemaVersion":1,"source":{},"model":"group","content":null}}`)}
	frames, err := adapter.Convert(t.Context(), ClientEvent, configure)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(frames[0].Payload, []byte(`"model":"upstream-model"`)) {
		t.Fatalf("model was not mapped: %s", frames[0].Payload)
	}
	configure.Payload = bytes.Replace(configure.Payload, []byte(`"group"`), []byte(`"unauthorized"`), 1)
	if _, err := adapter.Convert(t.Context(), ClientEvent, configure); !hasIssueCode(err, InvalidAssociation) {
		t.Fatalf("session changed authorized model: %v", err)
	}
}
