package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/hertz-contrib/websocket"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
)

type gatewaySessionSet struct {
	mu        sync.Mutex
	workers   sync.WaitGroup
	active    map[uint64]context.CancelFunc
	next      uint64
	isStopped bool
}

func (sessions *gatewaySessionSet) begin(ctx context.Context) (context.Context, func(), error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if sessions.isStopped {
		return nil, nil, fmt.Errorf("gateway sessions are shutting down")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if sessions.active == nil {
		sessions.active = map[uint64]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	sessions.next++
	id := sessions.next
	sessions.active[id] = cancel
	sessions.workers.Add(1)
	return ctx, func() {
		cancel()
		sessions.mu.Lock()
		delete(sessions.active, id)
		sessions.mu.Unlock()
		sessions.workers.Done()
	}, nil
}

func (sessions *gatewaySessionSet) stop() {
	sessions.mu.Lock()
	sessions.isStopped = true
	for _, cancel := range sessions.active {
		cancel()
	}
	sessions.mu.Unlock()
	sessions.workers.Wait()
}

func gatewaySessionOperation(compiled *protocol.Compiled, method, path string) (protocol.Operation, bool) {
	for _, operation := range compiled.Operations() {
		if _, isMatch := protocol.MatchOperationPath(operation.Path, path); isMatch && operation.Method == method && operation.Kind == "session" && operation.Transport == protocol.WebSocket {
			return operation, true
		}
	}
	return protocol.Operation{}, false
}

func (s *Server) serveGatewaySession(ctx context.Context, c *app.RequestContext, view protocol.RegistryView, ingress *protocol.Compiled, path string, handshake []byte) {
	start := time.Now()
	record := s.initUsageRecord(ctx, c, start, handshake, builtin.FormatType(ingress.Identity().Family))
	record.IngressRevision, record.SourceEndpoint, record.SourceFormat = ingress.Hash(), string(c.Path()), ingress.Identity().DefinitionID
	record.Stream, record.RelayMode = true, "protocol_v2_websocket"
	var adapter *protocol.SessionAdapter
	var releaseSession func()
	var releases []func()
	transferred := false
	cleanup := sync.OnceFunc(func() {
		for _, release := range releases {
			release()
		}
		s.recordGatewaySession(record, adapter)
		if releaseSession != nil {
			releaseSession()
		}
	})
	defer func() {
		if !transferred {
			cleanup()
		}
	}()
	plan, err := s.prepareGatewayPlan(ctx, c, view, ingress, path, handshake, record)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusBadRequest, err)
		return
	}
	candidate := plan.candidates[0]
	releases = append(releases, s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash()), s.protocolUses.acquire(ingress.Identity().DefinitionID, ingress.Hash()))
	prepareCandidateRecord(record, candidate, plan.request)
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		s.failGateway(ctx, c, record, http.StatusForbidden, err)
		return
	}
	request := candidate.prepared.Clone()
	request.Model = protocol.StringValue(candidate.model.Identifier())
	sessionOptions := protocol.EvaluationContext{Scope: candidate.scope, Diagnostics: &protocol.DiagnosticSink{}}
	releases = append(releases, func() { record.appendConversionIssues(sessionOptions.Diagnostics.Issues()) })
	if candidate.model.CacheSynthesis {
		protocol.SynthesizeCacheBreakpoints(request, candidate.compiled.Capabilities(protocol.EncodeRequest), sessionOptions)
	}
	s.estimateGatewayAttempt(record, request)
	record.SystemStructure.After = systemStructure(request)
	body, err := candidate.compiled.EncodeRequest(ctx, request, sessionOptions)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusBadRequest, err)
		return
	}
	upstreamRequest, err := protocol.BuildSessionHandshake(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Identifier()})
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusBadRequest, err)
		return
	}
	adapter, err = protocol.NewSessionAdapter(ingress, candidate.compiled, plan.operation, candidate.operation, candidate.binding, candidate.scope, plan.request.Model)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusBadRequest, err)
		return
	}
	setRecordModel(record, candidate.model, builtin.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.OutgoingBody = record.sanitizeBody(body)
	release, err := s.limiter.Acquire(plan.group, s.estimateProtocolTokens(candidate.prepared))
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusTooManyRequests, err)
		return
	}
	releases = append(releases, release)
	ctx, finish, err := s.gatewaySessions.begin(ctx)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusServiceUnavailable, err)
		return
	}
	releaseSession = finish
	connection := c.GetConn()
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	releases = append(releases, func() { stopConnection() })
	upgrader := websocket.HertzUpgrader{HandshakeTimeout: time.Duration(plan.operation.Session.CloseMillis) * time.Millisecond}
	err = upgrader.Upgrade(c, func(conn *websocket.Conn) {
		defer cleanup()
		client := newWebsocketSession(conn, connection, *plan.operation.Session)
		defer client.Close()
		stopSession := context.AfterFunc(ctx, func() { _ = client.Close() })
		defer stopSession()
		s.runGatewaySession(ctx, record, adapter, plan, candidate, client, upstreamRequest)
	})
	if err != nil {
		record.StatusCode, record.Error = c.Response.StatusCode(), "client WebSocket handshake rejected"
		return
	}
	transferred = true
	// A failed upgrade write skips the hijack callback. The native completion
	// hook releases its admission and revision pins on that path as well.
	onHTTPComplete(c, cleanup)
}

func (s *Server) runGatewaySession(ctx context.Context, record *usageRecord, adapter *protocol.SessionAdapter, plan *gatewayPlan, candidate gatewayCandidate, client protocol.SessionConnection, upstreamRequest *http.Request) {
	deadline, stop := context.WithTimeout(ctx, time.Duration(candidate.operation.Session.CloseMillis)*time.Millisecond)
	upstream, err := s.protocolTransport.DialProtocolSession(deadline, upstreamRequest, *candidate.operation.Session)
	stop()
	if err != nil {
		record.StatusCode, record.Error = http.StatusBadGateway, err.Error()
		deadline, stop := context.WithTimeout(ctx, time.Duration(plan.operation.Session.CloseMillis)*time.Millisecond)
		defer stop()
		if frame, encodeErr := adapter.EncodeFailure(deadline, err); encodeErr == nil {
			_ = client.Write(deadline, frame)
		}
		return
	}
	record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
	err = protocol.RunSession(ctx, client, upstream, *plan.operation.Session, *candidate.operation.Session, adapter)
	if err != nil {
		record.StatusCode, record.Error = http.StatusBadGateway, err.Error()
		if errors.Is(err, context.Canceled) {
			record.StatusCode, record.ErrorKind = statusClientClosedRequest, ErrorKindClientCanceled
		}
		var conversion *protocol.ConversionError
		if errors.As(err, &conversion) {
			record.appendConversionIssues(conversion.Issues)
		}
	} else if adapter.HasFailed() {
		record.StatusCode, record.Error = http.StatusBadGateway, "upstream session failed"
	} else {
		s.affinity.set(record.KeyHash, plan.group.ID, candidate.model.Name)
	}
}

func (s *Server) recordGatewaySession(record *usageRecord, adapter *protocol.SessionAdapter) {
	if adapter == nil || len(adapter.Responses()) == 0 {
		s.recordUsage(record)
		return
	}
	for _, response := range adapter.Responses() {
		entry := *record
		digest := sha256.Sum256([]byte(record.RequestID + nulSeparator + response.ID))
		entry.RequestID = record.RequestID + "_" + hex.EncodeToString(digest[:8])
		entry.ProtocolResponseID = response.ID
		entry.Usage, entry.UsageDetail = usageTokenUsage{}, usageDetail{}
		entry.ProtocolUsage = nil
		entry.StatusCode, entry.Error = http.StatusOK, ""
		switch response.Terminal {
		case protocol.ResponseFinished:
		case protocol.OperationCancelled:
			entry.StatusCode, entry.ErrorKind = statusClientClosedRequest, ErrorKindClientCanceled
		default:
			entry.StatusCode, entry.Error = http.StatusBadGateway, "response ended without a successful terminal"
		}
		updateRecordProtocolUsage(&entry, response.Usage)
		if response.Usage != nil {
			total := int64(0)
			if response.Usage.Total != nil {
				total = response.Usage.Total.Count
			} else {
				if response.Usage.Input != nil {
					total += response.Usage.Input.Count
				}
				if response.Usage.Output != nil {
					total += response.Usage.Output.Count
				}
			}
			s.limiter.Adjust(record.GroupID, int(total), usageDayKey(record.StartedAt))
		}
		s.recordUsage(&entry)
	}
}
