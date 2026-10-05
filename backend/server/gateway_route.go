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
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type gatewayCandidate struct {
	model         config.ModelRef
	compiled      *protocol.Compiled
	binding       protocol.Binding
	operation     protocol.Operation
	operationName string
	scope         protocol.Scope
	combinations  []protocol.CombinationReport
}

type gatewayPlan struct {
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
	bindings, err := s.store.ListProtocolBindings(c.Request.Context())
	if err != nil {
		return nil, err
	}
	plan := &gatewayPlan{ingress: ingress}
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
		matched, candidateIssues := matchGatewayCombination(ingress, candidate, request, constrained)
		if len(candidateIssues) > 0 {
			issues = append(issues, candidateIssues...)
			continue
		}
		candidate.binding = matched
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

func verifyGatewayBinding(ctx context.Context, view protocol.RegistryView, upstream *protocol.Compiled, capabilities protocol.CapabilitySet) []protocol.CombinationReport {
	reports := []protocol.CombinationReport{}
	for _, id := range view.IDs() {
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
	var diagnostics []protocol.ConversionIssue
	contractHash := protocol.CapabilityContractHash(candidate.binding.Capabilities)
	for _, report := range candidate.combinations {
		if report.SourceHash != ingress.Hash() || report.TargetHash != candidate.compiled.Hash() || report.CompilerVersion != protocol.CompilerVersion || report.Kind != protocol.OfflineVerification || report.BindingHash != contractHash {
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
		if issues := protocol.CheckRoute(request, candidate.compiled, matched, candidate.scope, candidate.operation.Transport); len(issues) > 0 {
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

func modelProtocolScope(model config.ModelRef) protocol.Scope {
	account := sha256.Sum256([]byte(model.SourceID + nulSeparator + model.APIKey))
	return protocol.Scope{Provider: model.SourceID, Account: hex.EncodeToString(account[:]), Model: model.Name}
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
	if !model.ToolsCapable || (group.ToolsCapable != nil && !*group.ToolsCapable) {
		for _, capability := range []protocol.Capability{protocol.FunctionToolsCapability, protocol.FreeTextToolsCapability, protocol.ServerToolsCapability} {
			capabilities[capability] = false
		}
	}
	if !model.VisionCapable || (group.VisionCapable != nil && !*group.VisionCapable) {
		for _, capability := range []protocol.Capability{protocol.ImagesCapability, protocol.AudioCapability, protocol.VideoCapability} {
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
