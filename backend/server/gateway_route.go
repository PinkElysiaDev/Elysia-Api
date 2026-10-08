package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type gatewayCandidate struct {
	conversion    *protocol.CompiledConversion
	routeBinding  protocol.Binding
	prepared      *protocol.Request
	continuation  *gatewayContinuation
	model         config.ModelRef
	compiled      *protocol.Compiled
	binding       protocol.Binding
	operation     protocol.Operation
	operationName string
	scope         protocol.Scope
	combinations  []protocol.CombinationReport
}

type gatewayPlan struct {
	carriers   []string
	session    string
	group      *config.ModelGroupConfig
	request    *protocol.Request
	candidates []gatewayCandidate
	ingress    *protocol.Compiled
	operation  protocol.Operation
}

func (s *Server) prepareGatewayPlan(c *gin.Context, view protocol.RegistryView, ingress *protocol.Compiled, path string, body []byte, record *usageRecord) (*gatewayPlan, error) {
	if !ingress.Supports(protocol.DecodeRequest) {
		return nil, gatewayIssue(ingress.Identity(), protocol.UnsupportedCapability, "/directions", "protocol is not a client ingress")
	}
	policies, bindings, _, err := s.store.ConversionSnapshot(c.Request.Context())
	if err != nil {
		return nil, err
	}
	plan := &gatewayPlan{ingress: ingress, session: continuationSession(c, body)}
	body, plan.carriers, err = builtin.ExtractContinuationCarriers(body, ingress.Identity().Family, ingress.ResourceLimits().BufferBytes)
	if err != nil {
		return nil, err
	}
	options := protocol.EvaluationContext{Values: protocol.Object{"path": protocol.StringValue(path)}}
	for _, operation := range ingress.Operations() {
		if parameters, matches := protocol.MatchOperationPath(operation.Path, path); matches {
			for name, value := range parameters {
				options.Values[name] = protocol.StringValue(value)
			}
		}
	}
	options.ResolveRequestScope = func(request *protocol.Request) (protocol.Scope, error) {
		return s.bindGatewayRequest(c, view, bindings, plan, path, request, record)
	}
	request, err := ingress.DecodeRequest(c.Request.Context(), body, options)
	if err != nil {
		return nil, err
	}
	plan.request = request
	if err := ingress.CheckOperationInput(plan.operation, body); err != nil {
		return nil, err
	}
	if plan.operation.Transport != protocol.WebSocket {
		if request.Parameters == nil {
			request.Parameters = protocol.Object{}
		}
		request.Parameters["stream"], err = protocol.EncodeValue(plan.operation.Transport != protocol.HTTPJSON)
		if err != nil {
			return nil, err
		}
	}
	var issues []protocol.ConversionIssue
	var eligible []gatewayCandidate
	for _, candidate := range plan.candidates {
		if plan.operation.Transport == protocol.WebSocket {
			if err := protocol.CheckSessionCompatibility(plan.operation, candidate.operation); err != nil {
				var conversion *protocol.ConversionError
				if errors.As(err, &conversion) {
					issues = append(issues, conversion.Issues...)
				}
				continue
			}
		}
		constrained := constrainGatewayCapabilities(candidate.binding, candidate.model, plan.group, bindings)
		candidate.conversion, err = resolveGatewayConversion(policies, bindings, ingress, candidate.compiled, candidate.model, candidate.operationName, candidate.operation.Transport)
		if err != nil {
			return nil, err
		}
		if err = s.loadProviderConversionEvidence(c.Request.Context(), candidate.conversion, candidate.model, candidate.compiled); err != nil {
			return nil, err
		}
		sink := &protocol.DiagnosticSink{}
		candidate.continuation, err = s.newGatewayContinuation(c, plan, candidate, sink)
		if err != nil {
			return nil, err
		}
		restored := request.Clone()
		if err = s.restoreGatewayContinuation(c, candidate.continuation, restored, plan.carriers); err != nil {
			return nil, err
		}
		candidate.prepared, err = candidate.conversion.Request(c.Request.Context(), restored, candidate.conversionContext(ingress, false), sink)
		record.appendConversionIssues(sink.Issues())
		if err != nil {
			return nil, err
		}
		matched, candidateIssues := matchGatewayCombination(ingress, candidate, candidate.prepared, constrained)
		if len(candidateIssues) > 0 {
			issues = append(issues, candidateIssues...)
			continue
		}
		candidate.binding = constrained
		candidate.routeBinding = matched
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return nil, protocol.IssuesError(issues)
	}
	plan.candidates = eligible
	return plan, nil
}

func makeGatewayCandidate(view protocol.RegistryView, bindings []storage.ProtocolBinding, model config.ModelRef, transport protocol.Transport, kind string) (gatewayCandidate, *protocol.ConversionError) {
	candidate := gatewayCandidate{model: model}
	entry, exists := selectProtocolBinding(bindings, model)
	if !exists {
		return candidate, gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/binding", "model has no protocol binding")
	}
	if entry.Unbound {
		return candidate, gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/binding", "模型已取消协议绑定，请先选择协议")
	}
	binding := entry.Binding
	compiled, _ := view.Pin(binding.ProtocolID)
	if issues := protocol.CheckBinding(binding, compiled); len(issues) > 0 {
		return candidate, &protocol.ConversionError{Issues: issues}
	}
	var operation *protocol.Operation
	if kind == "generate" && binding.Wait != nil && compiled.Operations()[binding.Operation].Kind == "submit" {
		kind = "submit"
	}
	for name, entry := range compiled.Operations() {
		isCompatibleStream := (transport == protocol.SSE || transport == protocol.NDJSON) && (entry.Transport == protocol.SSE || entry.Transport == protocol.NDJSON)
		if entry.Kind != kind || (entry.Transport != transport && !isCompatibleStream) || !slices.Contains(binding.Transports, entry.Transport) || (binding.Operation != "" && name != binding.Operation) {
			continue
		}
		if operation != nil {
			return candidate, gatewayIssue(compiled.Identity(), protocol.InvalidDefinition, "/operations", "multiple upstream generation operations require an explicit selection")
		}
		value := entry
		operation = &value
		candidate.operationName = name
	}
	if operation == nil {
		return candidate, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/operations", "upstream has no operation for the requested transport")
	}
	candidate.binding, candidate.compiled, candidate.operation, candidate.scope = binding, compiled, *operation, modelProtocolScope(model)
	candidate.combinations = entry.Combinations
	return candidate, nil
}

func verifyGatewayBinding(ctx context.Context, view protocol.RegistryView, upstream *protocol.Compiled, capabilities protocol.CapabilitySet, excludedIDs ...string) []protocol.CombinationReport {
	reports := []protocol.CombinationReport{}
	for _, id := range view.IDs() {
		if slices.Contains(excludedIDs, id) {
			continue
		}
		ingress, _ := view.Pin(id)
		if ingress.Supports(protocol.DecodeRequest) && (ingress.Supports(protocol.EncodeResponse) || ingress.Supports(protocol.EncodeEvent)) {
			reports = append(reports, protocol.VerifyBindingProfiles(ctx, ingress, upstream, capabilities)...)
		}
	}
	return reports
}

func hasPassingGatewayCombination(reports []protocol.CombinationReport) bool {
	for _, report := range reports {
		if report.Passed {
			return true
		}
	}
	return false
}

func matchGatewayCombination(ingress *protocol.Compiled, candidate gatewayCandidate, request *protocol.Request, constrained protocol.Binding) (protocol.Binding, []protocol.ConversionIssue) {
	// The actual upstream contract is mandatory even when a compatibility
	// profile omits metadata restored by the authenticated recovery channel.
	if issues := protocol.CheckRoute(request, candidate.compiled, constrained, candidate.scope, candidate.operation.Transport); len(issues) > 0 {
		return protocol.Binding{}, issues
	}
	profileRequest := request
	if candidate.continuation != nil && len(candidate.continuation.restored) > 0 {
		profileRequest = request.Clone()
		var strip func([]protocol.Node)
		strip = func(nodes []protocol.Node) {
			for i := range nodes {
				n := &nodes[i]
				if candidate.continuation.restored[protocol.ContinuationNodeDigest(*n)] {
					n.Resources = slices.DeleteFunc(n.Resources, func(r protocol.Resource) bool { return r.Kind == "signature" })
				}
				strip(n.Children)
			}
		}
		strip(profileRequest.Content)
	}
	var diagnostics []protocol.ConversionIssue
	contractHash := protocol.CapabilityContractHash(candidate.binding.Capabilities)
	for _, report := range candidate.combinations {
		if report.SourceHash != ingress.Hash() || report.TargetHash != candidate.compiled.Hash() || report.CompilerVersion != protocol.CompilerVersion || report.Kind != protocol.OfflineVerification || report.BindingHash != contractHash || report.SourceSamplesHash != ingress.SamplesHash() || report.TargetSamplesHash != candidate.compiled.SamplesHash() {
			continue
		}
		expectedContext := ""
		if candidate.conversion.ContextDependent() {
			expectedContext = candidate.conversion.WithVerificationContext(candidate.conversionContext(ingress, false)).ContextHash()
		}
		if report.ContextHash != expectedContext {
			continue
		}
		if report.PolicyHash != protocol.ConversionHash(candidate.conversion) {
			continue
		}
		if !report.Passed {
			diagnostics = append(diagnostics, report.Issues...)
			continue
		}
		matched := constrained
		matched.Capabilities = protocol.CapabilitySet{}
		for capability, supported := range constrained.Capabilities {
			matched.Capabilities[capability] = supported && report.Capabilities[capability]
		}
		if issues := protocol.CheckRoute(profileRequest, candidate.compiled, matched, candidate.scope, candidate.operation.Transport); len(issues) > 0 {
			diagnostics = append(diagnostics, issues...)
			continue
		}
		return matched, nil
	}
	if len(diagnostics) == 0 {
		diagnostics = gatewayIssue(candidate.compiled.Identity(), protocol.VerificationRequired, "/binding/combinations", "ingress/upstream revisions require paired offline verification for the current binding; save the binding again").Issues
	}
	return protocol.Binding{}, diagnostics
}

func (candidate gatewayCandidate) conversionContext(ingress *protocol.Compiled, response bool) protocol.ConversionContext {
	source, target := ingress.Identity(), candidate.compiled.Identity()
	if response {
		source, target = target, source
	}
	recoverable := map[string]bool{}
	if candidate.continuation != nil {
		for key := range candidate.continuation.saved {
			if strings.HasPrefix(key, "signature:") {
				recoverable[key] = true
				continue
			}
			if i := strings.IndexByte(key, ':'); i >= 0 {
				recoverable[key[i+1:]] = true
			}
		}
	}
	return protocol.ConversionContext{Scope: candidate.scope, Recoverable: recoverable, Source: source, Target: target, Model: candidate.model.Identifier(), Operation: candidate.operationName, Transport: candidate.operation.Transport}
}

func modelProtocolScope(model config.ModelRef) protocol.Scope {
	account := sha256.Sum256([]byte(model.SourceID + nulSeparator + model.APIKey))
	return protocol.Scope{Provider: model.SourceID, Account: hex.EncodeToString(account[:]), Model: model.Identifier()}
}

func constrainGatewayCapabilities(binding protocol.Binding, model config.ModelRef, group *config.ModelGroupConfig, bindings []storage.ProtocolBinding) protocol.Binding {
	capabilities := protocol.CapabilitySet{}
	for capability, supported := range binding.Capabilities {
		capabilities[capability] = supported
	}
	for _, entry := range bindings {
		if entry.Kind != "group" || entry.GroupID != group.ID {
			continue
		}
		for capability := range capabilities {
			capabilities[capability] = capabilities[capability] && entry.Binding.Capabilities[capability]
		}
	}
	toolsAllowed := model.ToolsCapable && (group.ToolsCapable == nil || *group.ToolsCapable)
	mediaAllowed := model.VisionCapable && (group.VisionCapable == nil || *group.VisionCapable)
	if !toolsAllowed {
		for _, capability := range toolCapabilityFamily {
			capabilities[capability] = false
		}
	}
	if !mediaAllowed {
		for _, capability := range mediaCapabilityFamily {
			capabilities[capability] = false
		}
	}
	binding.Capabilities = capabilities
	return binding
}

func selectGatewayIngressOperation(compiled *protocol.Compiled, method, path string, request *protocol.Request) (protocol.Operation, error) {
	var choices []protocol.Operation
	for _, operation := range compiled.Operations() {
		_, isMatch := protocol.MatchOperationPath(operation.Path, path)
		if operation.Method == method && isMatch && (operation.Kind == "generate" || operation.Kind == "session" || operation.Kind == "submit") {
			choices = append(choices, operation)
		}
	}
	if len(choices) > 1 {
		var isStream bool
		if stream := request.Parameters["stream"]; !stream.IsZero() {
			if err := stream.Decode(&isStream); err != nil {
				return protocol.Operation{}, err
			}
		}
		choices = slices.DeleteFunc(choices, func(operation protocol.Operation) bool { return (operation.Transport != protocol.HTTPJSON) != isStream })
	}
	if len(choices) != 1 {
		return protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.InvalidDefinition, "/operations", "client path/method does not select one generation operation")
	}
	if choices[0].Transport == protocol.HTTPJSON && !compiled.Supports(protocol.EncodeResponse) {
		return protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/directions/encode_response", "HTTP ingress requires a response encoder")
	}
	if choices[0].Transport != protocol.HTTPJSON && !compiled.Supports(protocol.EncodeEvent) {
		return protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/directions/encode_event", "streaming ingress requires an event encoder")
	}
	return choices[0], nil
}

func (s *Server) bindGatewayRequest(c *gin.Context, view protocol.RegistryView, bindings []storage.ProtocolBinding, plan *gatewayPlan, path string, request *protocol.Request, record *usageRecord) (protocol.Scope, error) {
	ingress := plan.ingress

	operation, err := selectGatewayIngressOperation(ingress, c.Request.Method, path, request)
	if err != nil {
		return protocol.Scope{}, err
	}
	plan.operation = operation
	record.Stream = operation.Transport != protocol.HTTPJSON
	var groupName string
	if err := request.Model.Decode(&groupName); err != nil || strings.TrimSpace(groupName) == "" {
		return protocol.Scope{}, gatewayIssue(ingress.Identity(), protocol.InvalidInput, "/model", "request requires a model group")
	}
	record.RequestedModelGroup = groupName
	if !s.tokenAllowsGroup(c, groupName) {
		return protocol.Scope{}, &gatewayFailure{http.StatusForbidden, fmt.Errorf("API key is not allowed to access this model group")}
	}
	group, failure := s.validateModelGroup(groupName)
	if failure != nil {
		return protocol.Scope{}, &gatewayFailure{failure.Class.HTTPStatus(), failure}
	}
	plan.group = group
	setRecordGroup(record, group)
	for _, binding := range bindings {
		if binding.Kind != "group" || binding.GroupID != group.ID {
			continue
		}
		if binding.Unbound {
			return protocol.Scope{}, gatewayIssue(ingress.Identity(), protocol.UnsupportedCapability, "/binding", "model group is explicitly unbound; select a protocol before calling")
		}
		if err := protocol.IssuesError(protocol.CheckIngressBinding(binding.Binding, ingress)); err != nil {
			return protocol.Scope{}, err
		}
		if !slices.Contains(binding.Binding.Transports, operation.Transport) {
			return protocol.Scope{}, gatewayIssue(ingress.Identity(), protocol.UnsupportedCapability, "/binding/transports", "group binding disallows this client transport")
		}
	}
	if err := s.collectGatewayCandidates(view, bindings, plan, record); err != nil {
		return protocol.Scope{}, err
	}

	if !protocol.HasScopedResources(request) {
		return protocol.Scope{}, nil
	}
	scope := plan.candidates[0].scope
	for _, candidate := range plan.candidates {
		if scope != candidate.scope {
			return protocol.Scope{}, gatewayIssue(ingress.Identity(), protocol.ResourceScopeMismatch, "/resources", "resource provenance is ambiguous across model accounts; use a single scoped binding")
		}
	}
	return scope, nil
}

func (s *Server) collectGatewayCandidates(view protocol.RegistryView, bindings []storage.ProtocolBinding, plan *gatewayPlan, record *usageRecord) error {
	models := s.buildCandidates(plan.group)
	if sticky := s.affinity.get(record.KeyHash, plan.group.ID, time.Now()); sticky != "" {
		models = applyAffinity(models, sticky)
	}
	models = s.expandCandidatesByKeyStrategy(models)
	var diagnostics []protocol.ConversionIssue
	for _, model := range models {
		candidate, err := makeGatewayCandidate(view, bindings, model, plan.operation.Transport, plan.operation.Kind)
		if err != nil {
			diagnostics = append(diagnostics, err.Issues...)
			continue
		}
		plan.candidates = append(plan.candidates, candidate)
	}
	if len(plan.candidates) == 0 {
		if len(diagnostics) > 0 {
			return protocol.IssuesError(diagnostics)
		}
		return gatewayIssue(plan.ingress.Identity(), protocol.VerificationRequired, "/binding", "no model has a verified protocol binding for this transport")
	}
	return nil
}
