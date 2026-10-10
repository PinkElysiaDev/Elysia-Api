package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type gatewayContinuation struct {
	codec                            *protocol.ContinuationCodec
	owner, session, parent, scopeKey string
	scope                            protocol.Scope
	identity                         protocol.Identity
	settings                         protocol.ContinuationSettings
	strict                           bool
	requireCarrier                   bool
	conversion                       *protocol.CompiledConversion
	route                            protocol.ConversionContext
	tokens                           []string
	tokenBytes                       int
	saved                            map[string]bool
	restored                         map[string]bool
	sink                             *protocol.DiagnosticSink
}

func continuationSession(c *gin.Context, body []byte) string {
	for _, name := range []string{"x-elysia-session-id", "x-claude-code-session-id", "claude-code-session-id", "x-session-id", "session_id"} {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" && len(value) <= 512 {
			return value
		}
	}
	v, err := protocol.ParseValue(body)
	if err != nil {
		return ""
	}
	fields, _ := v.ReadObject()
	metadata, _ := fields["metadata"].ReadObject()
	var session string
	if metadata["session_id"].Decode(&session) == nil && session != "" && len(session) <= 512 {
		return session
	}
	var user string
	if metadata["user_id"].Decode(&user) == nil {
		if i := strings.LastIndex(user, "_session_"); i >= 0 {
			session = user[i+9:]
			if session != "" && len(session) <= 512 {
				return session
			}
		}
	}
	return ""
}

func (s *Server) newGatewayContinuation(c *gin.Context, plan *gatewayPlan, candidate gatewayCandidate, sink *protocol.DiagnosticSink) (*gatewayContinuation, error) {
	policy := candidate.conversion
	if policy == nil || policy.Policy.Continuation == nil {
		return nil, nil
	}
	if !policy.SignatureProjectionEnabled(protocol.ConversionResponse, candidate.conversionContext(plan.ingress, true)) &&
		!policy.SignatureProjectionEnabled(protocol.ConversionEvent, candidate.conversionContext(plan.ingress, true)) {
		return nil, nil
	}
	if plan.ingress.Identity().Family == candidate.compiled.Identity().Family && plan.ingress.Identity().WireVersion == candidate.compiled.Identity().WireVersion {
		return nil, nil
	}
	codec, _ := protocol.NewContinuationCodec(s.config.GetDBEncryptionKey())
	if codec == nil {
		return &gatewayContinuation{identity: candidate.compiled.Identity(), strict: policy.Policy.Mode == "strict", sink: sink}, nil
	}
	sum := sha256.Sum256([]byte(extractAccessToken(c.Request)))
	owner := codec.Digest(hex.EncodeToString(sum[:]))
	scopeValue, _ := protocol.EncodeValue(candidate.scope)
	g := &gatewayContinuation{codec: codec, owner: owner, session: plan.session, scope: candidate.scope, scopeKey: codec.Digest(string(scopeValue.Bytes())), identity: candidate.compiled.Identity(), settings: *policy.Policy.Continuation, strict: policy.Policy.Mode == "strict", saved: map[string]bool{}, restored: map[string]bool{}, sink: sink}
	switch plan.ingress.Identity().Family {
	case "claude", "openai_chat", "openai_responses":
	default:
		g.settings.ClientCarrier = false
	}
	g.parent = g.historyDigest(plan.request.Content)
	return g, nil
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

func (s *Server) restoreGatewayContinuation(c *gin.Context, g *gatewayContinuation, request *protocol.Request, tokens []string) error {
	if g == nil {
		return nil
	}
	if g.codec == nil {
		if len(tokens) > 0 {
			return g.warning("continuation recovery requires the configured master key")
		}
		return nil
	}
	var records []protocol.ContinuationRecord
	used := map[string]bool{}
	for _, token := range tokens {
		r, err := g.codec.Open(token, g.settings.RecordBytes)
		if err != nil {
			if e := g.warning(err.Error()); e != nil {
				return e
			}
			continue
		}
		if r.Session != g.session || r.Owner != g.owner || !protocol.CheckScope(r.Scope, g.scope) {
			if e := g.warning("continuation session, owner or upstream scope differs"); e != nil {
				return e
			}
			continue
		}
		records = append(records, r)
	}
	var visit func([]protocol.Node, string, *int) error
	visit = func(nodes []protocol.Node, parent string, ordinal *int) error {
		for i := range nodes {
			n := &nodes[i]
			if n.Kind == protocol.MessageNode {
				var role string
				_ = n.Role.Decode(&role)
				if role != "assistant" {
					continue
				}
				if err := visit(n.Children, parent, ordinal); err != nil {
					return err
				}
				continue
			}
			position := *ordinal
			*ordinal++
			digest := protocol.ContinuationNodeDigest(*n)
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
				values, err := s.store.FindContinuation(c.Request.Context(), storage.StoredContinuation{Owner: g.owner, Session: g.session, ScopeKey: g.scopeKey, Parent: parent, Ordinal: position, MatchToolCall: byCall, Digest: g.codec.Digest(digest)})
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
				if e := g.warning("continuation association is ambiguous"); e != nil {
					return e
				}
				continue
			}
			if len(matches) == 1 {
				if matches[0].Session != g.session {
					if e := g.warning("continuation session differs"); e != nil {
						return e
					}
					continue
				}
				if err := protocol.RestoreContinuation(n, matches[0], g.owner, g.scope, g.identity); err != nil {
					if e := g.warning(err.Error()); e != nil {
						return e
					}
				} else {
					g.restored[digest] = true
					used[matches[0].ID] = true
				}
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
			parent = g.historyDigest(request.Content[:i])
			ordinal = 0
			inAssistant = true
		}
		if err := visit(request.Content[i:i+1], parent, &ordinal); err != nil {
			return err
		}
	}
	for _, r := range records {
		if !used[r.ID] {
			return g.warning("authenticated continuation no longer matches an unchanged history node, position and parent")
		}
	}
	return nil
}

func (s *Server) captureContinuationNode(c *gin.Context, g *gatewayContinuation, node protocol.Node, ordinal int) error {
	if g == nil || !protocol.HasContinuationState(node) {
		return nil
	}
	if g.codec == nil {
		return g.warning("signature recovery requires the configured master key")
	}
	if !g.settings.ClientCarrier && !g.settings.Persist {
		return g.warning("signature recovery is disabled")
	}
	if g.requireCarrier && !g.settings.ClientCarrier {
		if err := g.conversion.CheckIncludeCarrier(g.route, g.sink); err != nil {
			return err
		}
	}
	digest := protocol.ContinuationNodeDigest(node)
	key := fmt.Sprintf("%d:%s", ordinal, digest)
	if g.saved[key] {
		return nil
	}
	id, err := protocol.NewContinuationID()
	if err != nil {
		return g.warning("could not allocate continuation identity")
	}
	record := protocol.ContinuationRecord{Version: 1, ID: id, Owner: g.owner, Session: g.session, Scope: g.scope, Protocol: g.identity, Parent: g.parent, Ordinal: ordinal, Digest: digest, Node: node, ExpiresAt: time.Now().Unix() + g.settings.RetentionSeconds}
	token, err := g.codec.Seal(record, g.settings.RecordBytes)
	if err != nil {
		return g.warning(err.Error())
	}
	preserved := false
	if g.settings.Persist && g.session != "" {
		err = s.store.SaveContinuation(c.Request.Context(), storage.StoredContinuation{ID: id, Owner: g.owner, Session: g.session, ScopeKey: g.scopeKey, Parent: g.parent, Ordinal: ordinal, Digest: g.codec.Digest(digest), Ciphertext: token, ExpiresAt: record.ExpiresAt}, g.settings)
		if err != nil {
			if g.settings.ClientCarrier {
				g.sink.Add(protocol.ConversionIssue{Code: protocol.ContinuationUnavailable, Severity: protocol.SeverityWarning, Protocol: g.identity, Stage: "continuation", Path: "/continuation/persistence", Reason: "persistent continuation write failed; authenticated client carrier remains available"})
			} else if e := g.warning("persistent continuation write failed"); e != nil {
				return e
			}
		}
	}
	if g.settings.Persist && g.session != "" && err == nil {
		preserved = true
	}
	if g.settings.ClientCarrier {
		if g.tokenBytes+len(token) > protocol.DefaultLimits().BufferBytes {
			return g.warning("continuation carrier buffer limit exceeded")
		}
		g.tokens = append(g.tokens, token)
		g.tokenBytes += len(token)
		preserved = true
	}
	if !preserved {
		return g.warning("signature has no usable recovery channel")
	}
	g.saved[key] = true
	for _, res := range node.Resources {
		if protocol.IsContinuationResource(res) {
			g.saved[protocol.ContinuationResourceKey(res)] = true
		}
	}
	return nil
}

func (s *Server) captureContinuationResponse(c *gin.Context, g *gatewayContinuation, response *protocol.Response) error {
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
				if err := s.captureContinuationNode(c, g, n, ordinal); err != nil {
					return err
				}
				ordinal++
			}
		}
		return nil
	}
	return visit(response.Content)
}
