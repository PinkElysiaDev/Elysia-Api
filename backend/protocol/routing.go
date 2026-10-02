package protocol

import (
	"fmt"
	"slices"
)

// Binding constrains a model independently of its wire adapter's capabilities.
// RevisionHash prevents a later activation from silently changing this contract.
type Binding struct {
	ProtocolID   string        `json:"protocolId"`
	RevisionHash string        `json:"revisionHash"`
	Capabilities CapabilitySet `json:"capabilities"`
	Transports   []Transport   `json:"transports"`
	Operation    string        `json:"operation,omitempty"`
	Wait         *JobWait      `json:"wait,omitempty"`
}

// JobWait explicitly opts a synchronous ingress into bounded async waiting.
// OnTimeout is cancel or continue and also governs disconnected waiters.
type JobWait struct {
	TimeoutMillis int    `json:"timeoutMillis"`
	OnTimeout     string `json:"onTimeout"`
}

// CheckBinding rejects a model promise that its pinned adapter cannot fulfill.
func CheckBinding(binding Binding, compiled *Compiled) []ConversionIssue {
	return checkBinding(binding, compiled, EncodeRequest, DecodeResponse, DecodeEvent)
}

// CheckIngressBinding constrains an explicitly selected group ingress.
func CheckIngressBinding(binding Binding, compiled *Compiled) []ConversionIssue {
	return checkBinding(binding, compiled, DecodeRequest, EncodeResponse, EncodeEvent)
}

func checkBinding(binding Binding, compiled *Compiled, requestDirection, responseDirection, eventDirection Direction) []ConversionIssue {
	issue := ConversionIssue{Code: InvalidDefinition, Severity: SeverityError, Direction: EncodeRequest, Stage: "binding", Path: "/binding", Suggestion: "Select a verified revision and declare only the model's supported capabilities and transports."}
	if compiled == nil {
		issue.Code, issue.Reason = VerificationRequired, "binding references an inactive protocol"
		return []ConversionIssue{issue}
	}
	issue.Protocol = compiled.Identity()
	if binding.ProtocolID != compiled.Identity().DefinitionID || binding.RevisionHash != compiled.Hash() {
		issue.Code, issue.Reason = VerificationMismatch, "binding revision differs from the active protocol"
		return []ConversionIssue{issue}
	}
	if !compiled.Supports(requestDirection) {
		issue.Reason = fmt.Sprintf("binding requires %s", requestDirection)
		return []ConversionIssue{issue}
	}
	if binding.Capabilities == nil || len(binding.Transports) == 0 {
		issue.Reason = "model capabilities and transports must be explicit"
		return []ConversionIssue{issue}
	}
	var issues []ConversionIssue
	for _, capability := range sortedKeys(binding.Capabilities) {
		if !slices.Contains(CapabilityCatalog(), Capability(capability)) {
			entry := issue
			entry.Path, entry.Reason = "/binding/capabilities/"+string(capability), "unknown model capability"
			issues = append(issues, entry)
		} else if binding.Capabilities[Capability(capability)] && !compiled.capabilities[Capability(capability)] {
			entry := issue
			entry.Code, entry.Capability, entry.Reason = UnsupportedCapability, Capability(capability), "model binding exceeds protocol capabilities"
			issues = append(issues, entry)
		}
	}
	operations := compiled.Operations()
	if binding.Wait != nil {
		operation := operations[binding.Operation]
		wait := binding.Wait
		if operation.Kind != "submit" || wait.TimeoutMillis <= 0 || wait.TimeoutMillis > DefaultSessionIdleMillis || (wait.OnTimeout != "cancel" && wait.OnTimeout != "continue") || (wait.OnTimeout == "cancel" && (operation.Task == nil || operation.Task.Cancel == "")) {
			issue.Reason = "async waiting requires an explicit submit operation, bounded timeout and supported cancel/continue policy"
			issues = append(issues, issue)
		}
	}
	for _, transport := range binding.Transports {
		required := []Direction{responseDirection}
		switch transport {
		case SSE, NDJSON:
			required = append(required, eventDirection)
		case WebSocket:
			clientDirection := EncodeUpstreamEvent
			if requestDirection == DecodeRequest {
				clientDirection = DecodeClientEvent
			}
			required = []Direction{clientDirection, eventDirection}
		}
		for _, direction := range required {
			if !compiled.Supports(direction) {
				entry := issue
				entry.Reason = fmt.Sprintf("%s binding requires %s", transport, direction)
				issues = append(issues, entry)
			}
		}
		hasTransport := false
		for _, operation := range operations {
			if operation.Transport == transport && operation.Kind != "models" {
				hasTransport = true
			}
		}
		if !hasTransport {
			entry := issue
			entry.Reason = fmt.Sprintf("protocol has no %s generation operation", transport)
			issues = append(issues, entry)
		}
	}
	if binding.Operation != "" {
		if _, exists := operations[binding.Operation]; !exists {
			issue.Reason = "binding references an unknown operation"
			issues = append(issues, issue)
		}
	}
	return issues
}

// CheckClientEvent checks session commands against the fixed model contract.
// Configuring a session cannot enable tools/media excluded by the route binding.
func CheckClientEvent(event Event, compiled *Compiled, binding Binding, scope Scope) []ConversionIssue {
	return CheckEvent(event, bindingTarget(compiled, binding, EncodeUpstreamEvent, scope), compiled.limits)
}

// CheckRoute validates actual input against both model and adapter constraints.
// It does not change content or return a downgraded request.
func CheckRoute(request *Request, compiled *Compiled, binding Binding, scope Scope, transport Transport) []ConversionIssue {
	if issues := CheckBinding(binding, compiled); len(issues) > 0 {
		return issues
	}
	if !slices.Contains(binding.Transports, transport) {
		return []ConversionIssue{{Code: UnsupportedCapability, Severity: SeverityError, Protocol: compiled.Identity(), Direction: EncodeRequest, Stage: "routing", Path: "/transport", Reason: "model binding does not support the requested transport", Suggestion: "Select a model binding that supports this operation."}}
	}
	return CheckRequest(request, bindingTarget(compiled, binding, EncodeRequest, scope), compiled.limits)
}

// CheckModelResponse validates the actual upstream result against its model
// contract, including features implemented by the adapter but disabled here.
func CheckModelResponse(response *Response, compiled *Compiled, binding Binding, scope Scope) []ConversionIssue {
	target := bindingTarget(compiled, binding, DecodeResponse, scope)
	target.Direction = EncodeResponse
	return upstreamBindingIssues(CheckResponse(response, target, compiled.limits))
}

// CheckModelEvent applies the same model contract to each upstream event.
func CheckModelEvent(event Event, compiled *Compiled, binding Binding, scope Scope) []ConversionIssue {
	target := bindingTarget(compiled, binding, DecodeEvent, scope)
	target.Direction = EncodeEvent
	return upstreamBindingIssues(CheckEvent(event, target, compiled.limits))
}

func bindingTarget(compiled *Compiled, binding Binding, direction Direction, scope Scope) Target {
	capabilities := compiled.Capabilities(direction)
	for capability := range capabilities {
		capabilities[capability] = capabilities[capability] && binding.Capabilities[capability]
	}
	return Target{Protocol: compiled.Identity(), Direction: direction, Scope: scope, Capabilities: capabilities}
}

func upstreamBindingIssues(issues []ConversionIssue) []ConversionIssue {
	for index := range issues {
		issues[index].Code, issues[index].Stage = UpstreamContractViolation, "upstream_binding"
	}
	return issues
}

// HasScopedResources identifies requests requiring an unambiguous source
// account. Opaque nodes/tools also require account provenance before replay.
func HasScopedResources(request *Request) bool {
	hasCache := func(intents []CacheIntent) bool {
		for _, intent := range intents {
			if intent.Resource != nil {
				return true
			}
		}
		return false
	}
	var hasNodes func([]Node) bool
	hasNodes = func(nodes []Node) bool {
		for _, node := range nodes {
			if node.Kind == OpaqueNode || len(node.Resources) > 0 || hasCache(node.Cache) || hasNodes(node.Children) {
				return true
			}
		}
		return false
	}
	for _, tool := range request.Tools {
		if tool.Kind == ServerTool || tool.Kind == OpaqueTool || hasCache(tool.Cache) {
			return true
		}
	}
	return len(request.Resources) > 0 || hasCache(request.Cache) || hasNodes(request.Content)
}
