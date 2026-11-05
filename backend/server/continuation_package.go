package server

import (
	"context"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) newPackageGatewayContinuation(token string, plan *gatewayPlan, candidate gatewayCandidate, sink *protocol.DiagnosticSink) (*gatewayContinuation, error) {
	conversion := candidate.conversion
	phase, direction := protocol.ConversionResponse, protocol.EncodeResponse
	if plan.operation.Transport != protocol.HTTPJSON {
		phase, direction = protocol.ConversionEvent, protocol.EncodeEvent
	}
	route := candidate.conversionContext(plan.ingress, true)
	hasInput := len(plan.carriers) > 0
	hasInputRecovery := conversion.HasContinuationRecovery(protocol.ConversionRequest, candidate.conversionContext(plan.ingress, false))
	if hasInput && (conversion.Policy.Continuation == nil || !hasInputRecovery) {
		return nil, gatewayIssue(plan.ingress.Identity(), protocol.ContinuationUnavailable, "/continuation", "authenticated history requires an enabled continuation policy")
	}
	if conversion.Policy.Continuation == nil || !hasInput && !conversion.HasContinuationRecovery(phase, route) {
		return nil, nil
	}
	codec, _ := protocol.NewContinuationCodec(s.config.GetDBEncryptionKey())
	g := newContinuationOwner(codec, token, plan, candidate, sink)
	g.isPackage, g.phase, g.conversion, g.route = true, phase, conversion, route
	g.requireCarrier = plan.request.ClientOutput != nil && plan.request.ClientOutput.RequireContinuationState
	g.settings.ClientCarrier = g.settings.ClientCarrier && plan.ingress.HasContinuationCarrier(direction)
	var err error
	g.binding, err = plan.ingress.BindContinuation(direction, candidate.compiled.Identity(), candidate.scope, g.settings.ClientCarrier)
	if err != nil {
		return nil, err
	}
	g.route = route.WithContinuation(g.binding, g.requireCarrier)
	return g, nil
}

func (s *Server) capturePackageContinuationNode(ctx context.Context, g *gatewayContinuation, node protocol.Node, ordinal int) error {
	needsRecovery, err := g.conversion.NeedsContinuationRecovery(ctx, g.phase, node, g.route)
	if err != nil || !needsRecovery {
		return err
	}
	if g.codec == nil {
		return g.warning("signature recovery requires the configured master key")
	}
	if !g.settings.ClientCarrier && !g.settings.Persist {
		return g.warning("signature recovery is disabled")
	}
	digest := protocol.ContinuationNodeDigest(node)
	if g.binding.HasCaptured(ordinal, digest) {
		return nil
	}
	id, err := protocol.NewContinuationID()
	if err != nil {
		return g.warning("could not allocate continuation identity")
	}
	record := protocol.ContinuationRecord{Version: 1, ID: id, Owner: g.owner, Session: g.session, Scope: g.scope, Protocol: g.identity, Parent: g.parent, Ordinal: ordinal, Digest: digest, Node: node, ExpiresAt: time.Now().Unix() + g.settings.RetentionSeconds}
	sealed, err := g.codec.SealState(record, g.settings.RecordBytes)
	if err != nil {
		return g.warning(err.Error())
	}
	if g.settings.Persist && g.session != "" {
		err = s.store.SaveContinuation(ctx, storage.StoredContinuation{ID: id, Owner: g.owner, Session: g.session, ScopeKey: g.scopeKey, Parent: g.parent, Ordinal: ordinal, Digest: g.codec.Digest(digest), Ciphertext: sealed.Token(), ExpiresAt: record.ExpiresAt}, g.settings)
		if err == nil {
			sealed.MarkPersisted()
		} else if g.settings.ClientCarrier {
			g.sink.Add(protocol.ConversionIssue{Code: protocol.ContinuationUnavailable, Severity: protocol.SeverityWarning, Protocol: g.identity, Stage: "continuation", Path: "/continuation/persistence", Reason: "persistent continuation write failed; authenticated client carrier remains available"})
		} else {
			return g.warning("persistent continuation write failed")
		}
	}
	return g.binding.Append(sealed)
}
