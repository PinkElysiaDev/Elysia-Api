package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

type gatewayContinuation struct {
	binding                          *protocol.ContinuationBinding
	phase                            protocol.ConversionPhase
	codec                            *protocol.ContinuationCodec
	owner, session, parent, scopeKey string
	scope                            protocol.Scope
	identity                         protocol.Identity
	settings                         protocol.ContinuationSettings
	strict                           bool
	requireCarrier                   bool
	conversion                       *protocol.CompiledConversion
	route                            protocol.ConversionContext
	restored                         map[string]bool
	sink                             *protocol.DiagnosticSink
}

func newContinuationOwner(codec *protocol.ContinuationCodec, token string, plan *gatewayPlan, candidate gatewayCandidate, sink *protocol.DiagnosticSink) *gatewayContinuation {
	g := &gatewayContinuation{codec: codec, session: plan.session, scope: candidate.scope, identity: candidate.compiled.Identity(), settings: *candidate.conversion.Policy.Continuation, strict: candidate.conversion.Policy.Mode == "strict", restored: map[string]bool{}, sink: sink}
	if codec != nil {
		sum := sha256.Sum256([]byte(token))
		g.owner = codec.Digest(hex.EncodeToString(sum[:]))
		scopeValue, _ := protocol.EncodeValue(candidate.scope)
		g.scopeKey = codec.Digest(scopeValue.JSONString())
	}
	return g
}

func (g *gatewayContinuation) needsRecovery(tokens []string) bool {
	return g != nil && (len(tokens) > 0 || g.codec != nil && g.settings.Persist && g.session != "")
}

// Finish the outgoing association before preparation releases request history.
// Delivery retains only the digest, including during a slow upstream stream.
func (g *gatewayContinuation) prepareParent(nodes []protocol.Node) {
	if g == nil || g.codec == nil || !g.settings.ClientCarrier && !(g.settings.Persist && g.session != "") {
		return
	}
	if !g.conversion.HasContinuationRecovery(g.phase, g.route) {
		return
	}
	if g.parent == "" {
		g.parent = g.historyDigest(nodes)
	}
}

func (g *gatewayContinuation) warning(reason string) error {
	issue := protocol.ConversionIssue{Code: protocol.ContinuationUnavailable, Severity: protocol.SeverityWarning, Protocol: g.identity, Stage: "continuation", Path: "/continuation", Reason: reason}
	if g.strict {
		issue.Severity = protocol.SeverityError
		return protocol.IssuesError([]protocol.ConversionIssue{issue})
	}
	g.sink.Add(issue)
	return nil
}

// A recognized package carrier is an authenticated input, never a lossy hint.
func (g *gatewayContinuation) recoveryFailure(reason string) error {
	return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.ContinuationUnavailable, Severity: protocol.SeverityError, Protocol: g.identity, Stage: "continuation", Path: "/continuation", Reason: reason}})
}

func (g *gatewayContinuation) historyDigest(nodes []protocol.Node) string {
	var parts []string
	var visit func([]protocol.Node, string)
	visit = func(nodes []protocol.Node, role string) {
		for _, n := range nodes {
			if n.Kind == protocol.MessageNode {
				r := role
				_ = n.Role.Decode(&r)
				visit(n.Children, r)
			} else {
				if n.Kind == protocol.ToolCallNode {
					role = "assistant"
				}
				if n.Kind == protocol.ToolResultNode {
					role = "tool"
				}
				parts = append(parts, role+":"+protocol.ContinuationNodeDigest(n))
			}
		}
	}
	visit(nodes, "")
	return g.codec.Digest(strings.Join(parts, "\n"))
}

func (s *Server) restoreGatewayContinuation(ctx context.Context, g *gatewayContinuation, request *protocol.Request, tokens []string, history *continuationHistory) error {
	if g == nil {
		return nil
	}
	if !g.needsRecovery(tokens) {
		return request.AcceptContinuations(tokens)
	}
	if g.codec == nil {
		return g.recoveryFailure("continuation recovery requires the configured master key")
	}
	var records []protocol.ContinuationRecord
	used := map[string]bool{}
	for _, token := range tokens {
		r, err := g.codec.Open(token, g.settings.RecordBytes)
		if err != nil {
			return g.recoveryFailure(err.Error())
		}
		if r.Session != g.session || r.Owner != g.owner || !protocol.CheckScope(r.Scope, g.scope) {
			return g.recoveryFailure("continuation session, owner or upstream scope differs")
		}
		records = append(records, r)
	}
	if history == nil {
		history = &continuationHistory{}
	}
	if err := history.prepare(ctx, request.Content); err != nil {
		return err
	}
	parents := history.parents(g.codec)
	var visit func([]protocol.Node, []continuationHistoryNode, string, *int) error
	visit = func(nodes []protocol.Node, history []continuationHistoryNode, parent string, ordinal *int) error {
		for i := range nodes {
			n := &nodes[i]
			if n.Kind == protocol.MessageNode {
				var role string
				_ = n.Role.Decode(&role)
				if role != "assistant" {
					continue
				}
				if err := visit(n.Children, history[i].children, parent, ordinal); err != nil {
					return err
				}
				continue
			}
			position := *ordinal
			*ordinal++
			digest := history[i].digest
			// Tool identity and arguments are in the authenticated digest. Clients
			// may insert a null/empty text block before a tool; that changes only its
			// display position. Text and media still require the exact ordinal.
			byCall := n.Kind == protocol.ToolCallNode && !n.CallID.IsZero() && !n.CallID.IsNull()
			matches := []protocol.ContinuationRecord{}
			for _, r := range records {
				if r.Digest == digest && (byCall || r.Ordinal == position) && r.Parent == parent {
					matches = append(matches, r)
				}
			}
			if len(matches) == 0 && g.settings.Persist && g.session != "" {
				values, err := s.store.FindContinuation(ctx, storage.StoredContinuation{Owner: g.owner, Session: g.session, ScopeKey: g.scopeKey, Parent: parent, Ordinal: position, MatchToolCall: byCall, Digest: g.codec.Digest(digest)})
				if err != nil {
					if e := g.warning("persistent continuation lookup failed"); e != nil {
						return e
					}
				}
				for _, value := range values {
					r, e := g.codec.Open(value, g.settings.RecordBytes)
					if e == nil {
						matches = append(matches, r)
					}
				}
			}
			if len(matches) > 1 {
				return g.recoveryFailure("continuation association is ambiguous")
			}
			if len(matches) == 1 {
				if matches[0].Session != g.session {
					return g.recoveryFailure("continuation session differs")
				}
				if err := protocol.RestoreContinuation(n, matches[0], g.owner, g.scope, g.identity); err != nil {
					return g.recoveryFailure(err.Error())
				}
				g.restored[digest] = true
				used[matches[0].ID] = true
			}
		}
		return nil
	}
	// Contiguous assistant output items form one turn in both Messages and
	// Responses. Parallel calls share a parent but have distinct ordinals.
	parent := ""
	ordinal := 0
	inAssistant := false
	for i, n := range request.Content {
		var role string
		_ = n.Role.Decode(&role)
		if n.Kind == protocol.ToolCallNode || n.Kind == protocol.ReasoningNode {
			role = "assistant"
		}
		if role != "assistant" {
			inAssistant = false
			continue
		}
		if !inAssistant {
			parent = parents[i]
			ordinal = 0
			inAssistant = true
		}
		if err := visit(request.Content[i:i+1], history.nodes[i:i+1], parent, &ordinal); err != nil {
			return err
		}
	}
	for _, r := range records {
		if !used[r.ID] {
			return g.recoveryFailure("authenticated continuation no longer matches an unchanged history node, position and parent")
		}
	}
	g.parent = parents[len(request.Content)]
	return request.AcceptContinuations(tokens)
}

func (s *Server) captureContinuationResponse(ctx context.Context, g *gatewayContinuation, response *protocol.Response) error {
	if g == nil {
		return nil
	}
	ordinal := 0
	var visit func([]protocol.Node) error
	visit = func(nodes []protocol.Node) error {
		for _, n := range nodes {
			if n.Kind == protocol.MessageNode {
				if err := visit(n.Children); err != nil {
					return err
				}
			} else {
				if err := s.captureContinuationNode(ctx, g, n, ordinal); err != nil {
					return err
				}
				ordinal++
			}
		}
		return nil
	}
	return visit(response.Content)
}
