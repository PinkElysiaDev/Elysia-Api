package protocol

import (
	"fmt"
	"net/http"
)

const (
	DefaultSessionFrameBytes  = 1 << 20
	DefaultSessionQueueItems  = 32
	DefaultSessionQueueBytes  = 8 << 20
	DefaultSessionIdleMillis  = 120000
	DefaultSessionPingMillis  = 30000
	DefaultSessionCloseMillis = 5000
)

// MediaFormat is an already encoded binary wire format; no transcoding occurs.
type MediaFormat struct {
	Type   string `json:"type"`
	Format string `json:"format"`
}

// SessionConfig declares bounded duplex transport and permitted server behavior.
// Reconnection/replay is deliberately absent from the supported configuration.
type SessionConfig struct {
	FrameBytes               int          `json:"frameBytes"`
	QueueItems               int          `json:"queueItems"`
	QueueBytes               int          `json:"queueBytes"`
	IdleMillis               int          `json:"idleMillis"`
	PingMillis               int          `json:"pingMillis"`
	CloseMillis              int          `json:"closeMillis"`
	CanGenerateAutomatically bool         `json:"automaticResponses"`
	InputMedia               *MediaFormat `json:"inputMedia,omitempty"`
	OutputMedia              *MediaFormat `json:"outputMedia,omitempty"`
	QueryFields              []string     `json:"queryFields,omitempty"`
	HeaderFields             []string     `json:"headerFields,omitempty"`
}

// DefaultSessionConfig returns visible authoring defaults, not runtime repairs
// for invalid or missing configuration fields.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{FrameBytes: DefaultSessionFrameBytes, QueueItems: DefaultSessionQueueItems, QueueBytes: DefaultSessionQueueBytes, IdleMillis: DefaultSessionIdleMillis, PingMillis: DefaultSessionPingMillis, CloseMillis: DefaultSessionCloseMillis}
}

func checkSessionOperation(operation Operation, definition Definition, limits Limits) error {
	if operation.Transport != WebSocket {
		if operation.Session != nil || operation.Kind == "session" {
			return fmt.Errorf("session configuration requires WebSocket transport")
		}
		return nil
	}
	if operation.Kind != "session" || operation.Method != http.MethodGet || operation.Session == nil {
		return fmt.Errorf("WebSocket requires session kind, GET and explicit session limits")
	}
	if operation.Framing != nil {
		return fmt.Errorf("WebSocket framing is owned by its transport; declare media in session configuration")
	}
	options := operation.Session
	if err := checkHandshakeFields(options); err != nil {
		return err
	}
	if options.FrameBytes <= 0 || options.FrameBytes > limits.BufferBytes || options.QueueBytes < options.FrameBytes || options.QueueBytes > limits.BufferBytes || options.QueueItems <= 0 || options.QueueItems > limits.StateItems {
		return fmt.Errorf("session frame/queue limits must be positive and within engine limits")
	}
	if options.CloseMillis <= 0 || options.PingMillis <= options.CloseMillis || options.IdleMillis <= options.PingMillis || options.IdleMillis > DefaultSessionIdleMillis {
		return fmt.Errorf("session requires 0 < closeMillis < pingMillis < idleMillis <= %d", DefaultSessionIdleMillis)
	}
	if !definition.Capabilities[SessionsCapability] {
		return fmt.Errorf("WebSocket requires sessions capability")
	}
	for _, media := range []*MediaFormat{options.InputMedia, options.OutputMedia} {
		if media != nil && (media.Type == "" || media.Format == "" || !definition.Capabilities[RealtimeMediaCapability]) {
			return fmt.Errorf("binary media requires type, format and media.realtime capability")
		}
	}
	canIngress := hasDefinitionDirections(definition, DecodeRequest, DecodeClientEvent, EncodeEvent)
	canUpstream := hasDefinitionDirections(definition, EncodeRequest, EncodeUpstreamEvent, DecodeEvent)
	if !canIngress && !canUpstream {
		return fmt.Errorf("WebSocket requires a complete ingress or upstream session adapter")
	}
	return nil
}

func hasDefinitionDirections(definition Definition, directions ...Direction) bool {
	for _, direction := range directions {
		if _, exists := definition.Directions[direction]; !exists {
			return false
		}
	}
	return true
}

// CheckSessionCompatibility rejects binary transcoding and automatic generation
// mismatches before either WebSocket endpoint is opened.
func CheckSessionCompatibility(ingress, upstream Operation) error {
	if ingress.Transport != WebSocket || upstream.Transport != WebSocket || ingress.Session == nil || upstream.Session == nil {
		return streamIssue(UnsupportedCapability, "/transport", "session bridging requires two declared WebSocket operations")
	}
	for _, pair := range [][2]*MediaFormat{{ingress.Session.InputMedia, upstream.Session.InputMedia}, {ingress.Session.OutputMedia, upstream.Session.OutputMedia}} {
		if (pair[0] == nil) != (pair[1] == nil) || (pair[0] != nil && *pair[0] != *pair[1]) {
			return streamIssue(UnsupportedCapability, "/session/media", "binary session media formats differ; transcoding is not supported")
		}
	}
	if upstream.Session.CanGenerateAutomatically && !ingress.Session.CanGenerateAutomatically {
		return streamIssue(UnsupportedCapability, "/session/automaticResponses", "client protocol does not permit automatic upstream responses")
	}
	return nil
}
