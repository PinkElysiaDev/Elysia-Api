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

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
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

func (s *Server) serveGatewaySession(c *gin.Context, view protocol.RegistryView, ingress *protocol.Compiled, path string, handshake []byte) {
	start := time.Now()
	record := s.initUsageRecord(c, start, handshake, builtin.FormatType(ingress.Identity().Family))
	record.IngressRevision, record.SourceEndpoint, record.SourceFormat = ingress.Hash(), c.Request.URL.Path, ingress.Identity().DefinitionID
	record.Stream, record.RelayMode = true, "protocol_v2_websocket"
	var adapter *protocol.SessionAdapter
	var releaseSession func()
	defer func() {
		s.recordGatewaySession(record, adapter)
		if releaseSession != nil {
			releaseSession()
		}
	}()
	plan, err := s.prepareGatewayPlan(c, view, ingress, path, handshake, record)
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	candidate := plan.candidates[0]
	defer s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash())()
	defer s.protocolUses.acquire(ingress.Identity().DefinitionID, ingress.Hash())()
	prepareCandidateRecord(record, candidate, plan.request)
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		s.failGateway(c, record, http.StatusForbidden, err)
		return
	}
	request := plan.request.Clone()
	request.Model = protocol.StringValue(candidate.model.Name)
	sessionOptions := protocol.EvaluationContext{Scope: candidate.scope, Diagnostics: &protocol.DiagnosticSink{}}
	defer func() { record.appendConversionIssues(sessionOptions.Diagnostics.Issues()) }()
	if candidate.model.CacheSynthesis {
		protocol.SynthesizeCacheBreakpoints(request, candidate.compiled.Capabilities(protocol.EncodeRequest), sessionOptions)
	}
	record.SystemStructure.After = systemStructure(request)
	body, err := candidate.compiled.EncodeRequest(c.Request.Context(), request, sessionOptions)
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	upstreamRequest, err := protocol.BuildSessionHandshake(c.Request.Context(), candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Name})
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	adapter, err = protocol.NewSessionAdapter(ingress, candidate.compiled, plan.operation, candidate.operation, candidate.binding, candidate.scope, plan.request.Model)
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	setRecordModel(record, candidate.model, builtin.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.OutgoingBody = record.sanitizeBody(body)
	release, err := s.acquireRateLimit(plan.group, s.estimateProtocolTokens(plan.request))
	if err != nil {
		s.failGateway(c, record, http.StatusTooManyRequests, err)
		return
	}
	defer release()
	ctx, finish, err := s.gatewaySessions.begin(c.Request.Context())
	if err != nil {
		s.failGateway(c, record, http.StatusServiceUnavailable, err)
		return
	}
	releaseSession = finish
	client, err := relay.AcceptProtocolSession(c.Writer, c.Request, *plan.operation.Session)
	if err != nil {
		record.StatusCode, record.Error = c.Writer.Status(), "client WebSocket handshake rejected"
		return
	}
	defer client.Close()
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
	record.FirstByteMs = time.Since(start).Milliseconds()
	err = protocol.RunSession(ctx, client, upstream, *plan.operation.Session, *candidate.operation.Session, adapter)
	if err != nil {
		record.StatusCode, record.Error = http.StatusBadGateway, err.Error()
		if errors.Is(err, context.Canceled) {
			record.StatusCode, record.ErrorKind = statusClientClosedRequest, ErrorKindClientCanceled
		}
		var conversion *protocol.ConversionError
		if errors.As(err, &conversion) {
			record.ConversionIssues = conversion.Issues
		}
	} else if adapter.HasFailed() {
		record.StatusCode, record.Error = http.StatusBadGateway, "upstream session failed"
	} else {
		s.affinity.set(record.KeyHash, plan.group.ID, candidate.model.Name, time.Now())
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
			s.adjustTokenUsage(record.GroupID, int(total), usageDayKey(record.StartedAt))
		}
		s.recordUsage(&entry)
	}
}
