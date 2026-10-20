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

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type gatewayCandidate struct {
	encoding      *gatewayRequestEncoding
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
	bodyBytes     int
	carriers      []string
	session       string
	group         *config.ModelGroupConfig
	request       *protocol.Request
	candidates    []gatewayCandidate
	ingress       *protocol.Compiled
	operation     protocol.Operation
	operationName string
}

func (s *Server) prepareGatewayPlan(c *gin.Context, view protocol.RegistryView, ingress *protocol.Compiled, path string, body []byte, record *usageRecord) (*gatewayPlan, error) {
	if !ingress.Supports(protocol.DecodeRequest) {
		return nil, gatewayIssue(ingress.Identity(), protocol.UnsupportedCapability, "/directions", "protocol is not a client ingress")
	}
	conversions, err := s.pinGatewayConversions(c.Request.Context())
	if err != nil {
		return nil, err
	}
	bindings := conversions.bindings
	input, err := protocol.ParseValue(body)
	if err != nil {
		return nil, err
	}
	plan := &gatewayPlan{ingress: ingress, session: continuationSession(c, input), bodyBytes: len(body)}
	input, plan.carriers, err = builtin.ExtractContinuationCarriersValue(input, ingress.Identity().Family, ingress.RequestLimits(c.Request.Context()).BufferBytes)
	if err != nil {
		return nil, err
	}
	options := protocol.EvaluationContext{InterleavedThinking: protocol.OperationInterleavedThinking(protocol.Operation{Headers: map[string]string{"anthropic-beta": c.GetHeader("anthropic-beta")}}), Values: protocol.Object{"path": protocol.StringValue(path)}}
	for _, operation := range ingress.OperationRoutes() {
		if parameters, matches := protocol.MatchOperationPath(operation.Path, path); matches {
			for name, value := range parameters {
				options.Values[name] = protocol.StringValue(value)
			}
		}
	}
	options.ResolveRequestScope = func(request *protocol.Request) (protocol.Scope, error) {
		return s.bindGatewayRequest(c, view, bindings, plan, path, request, record)
	}
	request, err := ingress.DecodeRequestValue(c.Request.Context(), input, options)
	if err != nil {
		return nil, err
	}
	plan.request = request
	if err := ingress.CheckOperationValueInput(c.Request.Context(), plan.operationName, input); err != nil {
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
		candidate.conversion, err = conversions.resolve(ingress, candidate.compiled, candidate.model, candidate.operationName, candidate.operation.Transport)
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
		// Conversion owns its output. Only authenticated recovery needs a
		// mutable candidate-local input before that ownership boundary.
		restored := request
		if candidate.continuation != nil {
			restored = request.Clone()
		}
		if err = s.restoreGatewayContinuation(c, candidate.continuation, restored, plan.carriers); err != nil {
			return nil, err
		}
		candidate.prepared, err = candidate.conversion.Request(c.Request.Context(), restored, candidate.conversionContext(ingress, false), sink)
		if err == nil && candidate.continuation != nil {
			candidate.continuation.requireCarrier = candidate.prepared.ClientOutput.RequestsEncryptedReasoning()
			candidate.continuation.conversion = candidate.conversion
			candidate.continuation.route = candidate.conversionContext(ingress, true)
		}
		record.appendConversionIssues(sink.Issues())
		if err != nil {
			var failure *protocol.ConversionError
			if errors.As(err, &failure) && failure.CandidateOnly {
				issues = append(issues, failure.Issues...)
				record.appendConversionIssues(failure.Issues)
				continue
			}
			return nil, err
		}
		matched, candidateIssues := matchGatewayCombination(ingress, candidate, candidate.prepared, constrained)
		if len(candidateIssues) > 0 {
			issues = append(issues, candidateIssues...)
			record.appendConversionIssues(candidateIssues)
			continue
		}
		probe := candidate.prepared.Clone()
		probe.Model = protocol.StringValue(candidate.model.Identifier())
		probeOptions := protocol.EvaluationContext{Scope: candidate.scope, InterleavedThinking: protocol.OperationInterleavedThinking(candidate.operation), ClientOutput: probe.ClientOutput}
		canRetainEncoding := len(eligible) == 0 && !candidate.model.CacheSynthesis && candidate.operation.Kind == "generate" && candidate.compiled.CanReuseRequestEncoding()
		if canRetainEncoding {
			probeOptions.Diagnostics = &protocol.DiagnosticSink{}
		}
		encoded, encodeErr := candidate.compiled.EncodeRequestValue(c.Request.Context(), probe, probeOptions)
		if encodeErr == nil {
			encodeErr = candidate.compiled.CheckOperationValueInput(c.Request.Context(), candidate.operationName, encoded)
		}
		if encodeErr != nil {
			if c.Request.Context().Err() != nil {
				return nil, c.Request.Context().Err()
			}
			var failure *protocol.ConversionError
			if !errors.As(encodeErr, &failure) {
				return nil, encodeErr
			}
			issues = append(issues, failure.Issues...)
			record.appendConversionIssues(failure.Issues)
			continue
		}
		candidate.binding = constrained
		candidate.routeBinding = matched
		if canRetainEncoding {
			candidate.encoding = &gatewayRequestEncoding{value: encoded, request: probe, identity: candidate.compiled.Identity(), scope: probeOptions.Scope, limits: candidate.compiled.RequestLimits(c.Request.Context()), interleaved: probeOptions.InterleavedThinking, issues: probeOptions.Diagnostics.Issues()}
		}
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
	if kind == "generate" && binding.Wait != nil {
		for name, operation := range compiled.OperationRoutes() {
			if name == binding.Operation && operation.Kind == "submit" {
				kind = "submit"
			}
		}
	}
	for name, entry := range compiled.OperationRoutes() {
		isCompatibleStream := (transport == protocol.SSE || transport == protocol.NDJSON) && (entry.Transport == protocol.SSE || entry.Transport == protocol.NDJSON)
		if entry.Kind != kind || (entry.Transport != transport && !isCompatibleStream) || !slices.Contains(binding.Transports, entry.Transport) || (binding.Operation != "" && name != binding.Operation) {
			continue
		}
		if candidate.operationName != "" {
			return candidate, gatewayIssue(compiled.Identity(), protocol.InvalidDefinition, "/operations", "multiple upstream generation operations require an explicit selection")
		}
		candidate.operationName = name
	}
	if candidate.operationName == "" {
		return candidate, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/operations", "upstream has no operation for the requested transport")
	}
	operation, _ := compiled.Operation(candidate.operationName)
	candidate.binding, candidate.compiled, candidate.operation, candidate.scope = binding, compiled, operation, modelProtocolScope(model)
	candidate.combinations = entry.Combinations
	return candidate, nil
}

func (s *Server) verifyGatewayBinding(ctx context.Context, view protocol.RegistryView, binding storage.ProtocolBinding, excludedIDs ...string) ([]protocol.CombinationReport, error) {
	policies, bindings, _, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	replaced := false
	for i, old := range bindings {
		if old.Key() == binding.Key() {
			bindings[i], replaced = binding, true
		}
	}
	if !replaced {
		bindings = append(bindings, binding)
	}
	definitions := conversionDefinitions{}
	for _, id := range view.IDs() {
		definitions[id], _ = view.Pin(id)
	}
	verified, err := s.verifyConversionBindings(ctx, definitions, policies, bindings, conversionBindingScope{changed: &binding})
	if err != nil {
		return nil, err
	}
	for _, item := range verified {
		if item.Key() == binding.Key() {
			reports := slices.DeleteFunc(slices.Clone(item.Combinations), func(report protocol.CombinationReport) bool {
				for _, id := range excludedIDs {
					if source := definitions[id]; source != nil && source.Hash() == report.SourceHash {
						return true
					}
				}
				return false
			})
			return reports, nil
		}
	}
	return nil, fmt.Errorf("binding disappeared during verification")
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
					n.Resources = slices.DeleteFunc(n.Resources, func(r protocol.Resource) bool { return protocol.IsContinuationResource(r) })
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
		if issues := protocol.CheckCombinationRequest(profileRequest, report, candidate.compiled.Identity()); len(issues) > 0 {
			diagnostics = append(diagnostics, issues...)
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
			if strings.HasPrefix(key, "continuation-resource:") {
				recoverable[key] = true
				continue
			}
			if i := strings.IndexByte(key, ':'); i >= 0 {
				recoverable[key[i+1:]] = true
			}
		}
	}
	return protocol.ConversionContext{ResponseWire: response, Reasoning: candidate.binding.Reasoning, InterleavedThinking: protocol.OperationInterleavedThinking(candidate.operation), Scope: candidate.scope, Recoverable: recoverable, Source: source, Target: target, Model: candidate.model.Identifier(), Operation: candidate.operationName, Transport: candidate.operation.Transport}
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

func selectGatewayIngressOperation(compiled *protocol.Compiled, method, path string, request *protocol.Request) (string, protocol.Operation, error) {
	type choice struct {
		name   string
		stream bool
	}
	var choices []choice
	for name, operation := range compiled.OperationRoutes() {
		_, isMatch := protocol.MatchOperationPath(operation.Path, path)
		if operation.Method == method && isMatch && (operation.Kind == "generate" || operation.Kind == "session" || operation.Kind == "submit") {
			choices = append(choices, choice{name, operation.Transport != protocol.HTTPJSON})
		}
	}
	if len(choices) > 1 {
		var isStream bool
		if stream := request.Parameters["stream"]; !stream.IsZero() {
			if err := stream.Decode(&isStream); err != nil {
				return "", protocol.Operation{}, err
			}
		}
		choices = slices.DeleteFunc(choices, func(candidate choice) bool { return candidate.stream != isStream })
	}
	if len(choices) != 1 {
		return "", protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.InvalidDefinition, "/operations", "client path/method does not select one generation operation")
	}
	name := choices[0].name
	operation, _ := compiled.Operation(name)
	if operation.Transport == protocol.HTTPJSON && !compiled.Supports(protocol.EncodeResponse) {
		return "", protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/directions/encode_response", "HTTP ingress requires a response encoder")
	}
	if operation.Transport != protocol.HTTPJSON && !compiled.Supports(protocol.EncodeEvent) {
		return "", protocol.Operation{}, gatewayIssue(compiled.Identity(), protocol.UnsupportedCapability, "/directions/encode_event", "streaming ingress requires an event encoder")
	}
	return name, operation, nil
}

func (s *Server) bindGatewayRequest(c *gin.Context, view protocol.RegistryView, bindings []storage.ProtocolBinding, plan *gatewayPlan, path string, request *protocol.Request, record *usageRecord) (protocol.Scope, error) {
	ingress := plan.ingress

	name, operation, err := selectGatewayIngressOperation(ingress, c.Request.Method, path, request)
	if err != nil {
		return protocol.Scope{}, err
	}
	plan.operation = operation
	plan.operationName = name
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
	models := s.scheduler.Candidates(plan.group)
	if sticky := s.affinity.get(record.KeyHash, plan.group.ID); sticky != "" {
		models = applyAffinity(models, sticky)
	}
	models = s.scheduler.ExpandKeys(models)
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
