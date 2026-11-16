package protocol

import "fmt"

// Capability is an independently verifiable semantic feature.
type Capability string

const (
	TextCapability                      Capability = "text"
	OutputConstraintsCapability         Capability = "output.constraints"
	FunctionToolsCapability             Capability = "tools.function"
	FreeTextToolsCapability             Capability = "tools.free_text"
	ServerToolsCapability               Capability = "tools.server"
	NativeExtensionsCapability          Capability = "native_extensions"
	ControlStateCapability              Capability = "control.state"
	ImagesCapability                    Capability = "images"
	AudioCapability                     Capability = "audio"
	VideoCapability                     Capability = "video"
	DocumentsCapability                 Capability = "documents"
	ReasoningCapability                 Capability = "reasoning"
	ReasoningHistoryRetentionCapability Capability = "reasoning.history_retention"
	SignaturesCapability                Capability = "reasoning.signatures"
	EncryptedReasoningCapability        Capability = "reasoning.encrypted"
	CacheBreakpointsCapability          Capability = "cache.breakpoints"
	CacheKeysCapability                 Capability = "cache.keys"
	CacheRetentionCapability            Capability = "cache.retention"
	CacheResourcesCapability            Capability = "cache.resources"
	// CacheOptionsCapability covers the declared mode and minimum-lifetime knobs
	// that some providers expose separately from the maximum-retention setting.
	CacheOptionsCapability Capability = "cache.options"
	// CachePrewarmCapability covers a provider that will populate a cache ahead
	// of the first read, which is a distinct wire operation, not a breakpoint.
	CachePrewarmCapability  Capability = "cache.prewarm"
	SessionsCapability      Capability = "sessions"
	RealtimeMediaCapability Capability = "media.realtime"
	AsyncJobsCapability     Capability = "tasks.async"
	UsageCapability         Capability = "usage"
)

// CapabilitySet contains only features supported in the checked direction.
type CapabilitySet map[Capability]bool

// CheckToolAssociations checks only the history relation. It is also used on
// decoded final wire output, where account provenance is held by the caller.
func CheckToolAssociations(nodes []Node, limits Limits) []ConversionIssue {
	check := capabilityCheck{limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	var visit func([]Node, string, int)
	visit = func(items []Node, path string, depth int) {
		if depth > limits.Depth {
			check.add(LimitExceeded, path, "", "tool history depth exceeded")
			return
		}
		for i, n := range items {
			check.nodes++
			if check.nodes > limits.Nodes {
				check.add(LimitExceeded, path, "", "tool history node limit exceeded")
				return
			}
			at := fmt.Sprintf("%s/%d", path, i)
			if n.Kind == ServerToolNode {
				continue // Upstream execution cannot authorize a client result.
			}
			if n.Kind == ToolCallNode {
				check.call(n, at)
			}
			if n.Kind == ToolResultNode {
				check.result(n, at)
			}
			visit(n.Children, at+"/children", depth+1)
			visit(n.ReasoningContent, at+"/reasoningContent", depth+1)
		}
	}
	visit(nodes, "/content", 1)
	var issues []ConversionIssue
	for _, issue := range check.issues {
		if issue.Code == InvalidInput || issue.Code == InvalidAssociation || issue.Code == LimitExceeded {
			issues = append(issues, issue)
		}
	}
	return issues
}

type capabilityCheck struct {
	target  Target
	limits  Limits
	nodes   int
	issues  []ConversionIssue
	calls   map[string]struct{}
	results map[string]struct{}
}

// CheckRequest inspects actual content before routing. Unsupported data is
// diagnosed; it is never removed to fit the selected target's declarations.
func CheckRequest(request *Request, target Target, limits Limits) []ConversionIssue {
	if requestCheckUnchanged(request, target, limits) {
		return nil
	}
	check := capabilityCheck{target: target, limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	if err := limits.Validate(); err != nil {
		check.add(InvalidDefinition, "", "", err.Error())
		return check.issues
	}
	if request == nil || request.SchemaVersion != ProtocolCoreVersion {
		check.add(InvalidInput, "/schemaVersion", "", "missing request or unsupported semantic contract version")
		return check.issues
	}
	if !request.Parameters["outputConstraint"].IsZero() {
		check.require(OutputConstraintsCapability, outputConstraintPath)
	}
	if request.Reasoning != nil && !request.Reasoning.HistoryRetention.IsZero() {
		const path = "/reasoning/historyRetention"
		if !request.Reasoning.HistoryRetention.IsString("all") {
			check.add(InvalidInput, path, ReasoningHistoryRetentionCapability, "history retention must be all when present")
		} else {
			check.require(ReasoningHistoryRetentionCapability, path)
		}
	}
	var pathBuffer [256]byte
	root := diagnosticPath(pathBuffer[:0])
	for index, tool := range request.Tools {
		path := root.suffix("/tools").index(index)
		switch tool.Kind {
		case FunctionTool:
			check.requirePath(FunctionToolsCapability, path)
			check.requireNamePath(tool.Name, path.suffix("/name"))
		case FreeTextTool:
			check.requirePath(FreeTextToolsCapability, path)
			check.requireNamePath(tool.Name, path.suffix("/name"))
		case ServerTool:
			check.requirePath(ServerToolsCapability, path)
			check.nativePath(tool.Native, path)
		case OpaqueTool:
			check.requirePath(NativeExtensionsCapability, path)
			check.nativePath(tool.Native, path)
		default:
			check.addPath(InvalidInput, path.suffix("/kind"), "", "unknown tool kind")
		}
		check.cachePath(tool.Cache, path.suffix("/cache"))
	}
	check.contentPath(request.Content, root.suffix("/content"), 1, false)
	check.cachePath(request.Cache, root.suffix("/cache"))
	check.resourcesPath(request.Resources, root.suffix("/resources"))
	return check.issues
}

func (check *capabilityCheck) add(code IssueCode, path string, capability Capability, reason string) {
	check.issues = append(check.issues, ConversionIssue{Code: code, Severity: SeverityError, Protocol: check.target.Protocol,
		Direction: check.target.Direction, Stage: "check", Path: path, Capability: capability, Reason: reason,
		Suggestion: "Select a compatible target or correct the protocol definition and verify it again."})
}

func (check *capabilityCheck) require(capability Capability, path string) {
	if !check.target.Capabilities[capability] {
		check.add(UnsupportedCapability, path, capability, "target does not declare this capability")
	}
}

func (check *capabilityCheck) requireName(value Value, path string) {
	var name string
	if err := value.Decode(&name); err != nil || name == "" || value.IsNull() {
		check.add(InvalidInput, path, "", "a nonempty string is required")
	}
}

func (check *capabilityCheck) native(native *Native, path string) {
	if native == nil || !CanPreserveNative(native.Source, check.target) {
		check.add(UnsupportedNative, path, NativeExtensionsCapability, "opaque content lacks compatible native provenance")
		return
	}
	if !CheckScope(native.Source.Scope, check.target.Scope) {
		check.add(ResourceScopeMismatch, path, "", "native scope differs from target")
	}
}

func (check *capabilityCheck) contentPath(nodes []Node, path diagnosticPath, depth int, isServerArtifact bool) {
	if depth > check.limits.Depth {
		check.addPath(LimitExceeded, path, "", "content nesting exceeds configured limit")
		return
	}
	for index, node := range nodes {
		check.nodes++
		if check.nodes > check.limits.Nodes {
			check.addPath(LimitExceeded, path, "", "content node count exceeds configured limit")
			return
		}
		location := path.index(index)
		if isServerArtifact && (node.Kind == ToolCallNode || node.Kind == ToolResultNode || node.Kind == ServerToolNode) {
			check.addPath(InvalidInput, location.suffix("/kind"), ServerToolsCapability, "server artifacts cannot contain tool executions or client calls and results")
			continue
		}
		check.metadataSnapshotPath(node.Metadata, location.suffix("/metadata"))
		switch node.Kind {
		case MessageNode:
			check.requireNamePath(node.Role, location.suffix("/role"))
		case TextNode, RefusalNode:
			check.requirePath(TextCapability, location)
		case ImageNode:
			check.requirePath(ImagesCapability, location)
		case AudioNode:
			check.requirePath(AudioCapability, location)
		case VideoNode:
			check.requirePath(VideoCapability, location)
		case DocumentNode:
			check.requirePath(DocumentsCapability, location)
		case ReasoningNode:
			check.requirePath(ReasoningCapability, location)
			if node.ReasoningForm != "" && node.ReasoningForm != SummaryReasoning && node.ReasoningForm != StructuredReasoning {
				check.addPath(InvalidInput, location.suffix("/reasoningForm"), ReasoningCapability, "unknown reasoning representation")
			}
			if node.ReasoningForm != "" && !node.Payload.IsZero() {
				check.addPath(InvalidInput, location.suffix("/payload"), ReasoningCapability, "reasoning summary text belongs to ordered children")
			}
			if node.ReasoningForm == SummaryReasoning || node.ReasoningForm == StructuredReasoning {
				for i, child := range node.Children {
					if child.Kind != TextNode {
						check.addPath(InvalidInput, location.suffix("/children").index(i).suffix("/kind"), ReasoningCapability, "reasoning summary parts must be text")
					}
				}
			}
		case ToolCallNode:
			check.callPath(node, location)
		case ToolResultNode:
			check.resultPath(node, location)
		case ServerToolNode:
			check.serverToolPath(node, location)
		case OpaqueNode:
			check.requirePath(NativeExtensionsCapability, location)
			check.nativePath(node.Native, location)
		default:
			check.addPath(InvalidInput, location.suffix("/kind"), "", "unknown semantic content kind")
		}
		check.cachePath(node.Cache, location.suffix("/cache"))
		check.resourcesPath(node.Resources, location.suffix("/resources"))
		if node.ReasoningContent != nil {
			if node.Kind != ReasoningNode || node.ReasoningForm != StructuredReasoning {
				check.addPath(InvalidInput, location.suffix("/reasoningContent"), ReasoningCapability, "reasoning content requires structured reasoning")
			}
			for i, part := range node.ReasoningContent {
				var text string
				if part.Kind != TextNode || part.Payload.IsZero() || part.Payload.IsNull() || part.Payload.Decode(&text) != nil {
					check.addPath(InvalidInput, location.suffix("/reasoningContent").index(i), ReasoningCapability, "visible reasoning parts must contain string text")
				}
			}
			check.contentPath(node.ReasoningContent, location.suffix("/reasoningContent"), depth+1, isServerArtifact)
		}
		if len(node.Children) > 0 {
			check.contentPath(node.Children, location.suffix("/children"), depth+1, isServerArtifact || node.Kind == ServerToolNode)
		}
	}
}

func (check *capabilityCheck) callPath(node Node, path diagnosticPath) {
	check.requireNamePath(node.Name, path.suffix("/name"))
	check.requireNamePath(node.CallID, path.suffix("/callId"))
	var id string
	if err := node.CallID.Decode(&id); err == nil && id != "" {
		if _, hasCall := check.calls[id]; hasCall {
			check.addPath(InvalidAssociation, path.suffix("/callId"), "", "call identity was already used")
		}
		check.calls[id] = struct{}{}
	}
	if node.Input == nil || node.Input.Value.IsZero() {
		check.addPath(InvalidInput, path.suffix("/input"), "", "tool input is missing")
		return
	}
	switch node.Input.Kind {
	case JSONInput:
		check.requirePath(FunctionToolsCapability, path)
	case TextInput:
		check.requirePath(FreeTextToolsCapability, path)
		var text string
		if err := node.Input.Value.Decode(&text); err != nil || node.Input.Value.IsNull() {
			check.addPath(InvalidInput, path.suffix("/input/value"), "", "free text input must be a string")
		}
	default:
		check.addPath(InvalidInput, path.suffix("/input/kind"), "", "unknown tool input kind")
	}
}

func (check *capabilityCheck) resultPath(node Node, path diagnosticPath) {
	check.requireNamePath(node.CallID, path.suffix("/callId"))
	var id string
	if err := node.CallID.Decode(&id); err != nil || id == "" {
		return
	}
	if _, hasCall := check.calls[id]; !hasCall {
		check.addPath(InvalidAssociation, path.suffix("/callId"), "", "result has no preceding call in this history")
	}
	if _, hasResult := check.results[id]; hasResult {
		check.addPath(InvalidAssociation, path.suffix("/callId"), "", "call already has a result")
	}
	check.results[id] = struct{}{}
	if node.Payload.IsZero() && len(node.Children) == 0 {
		check.addPath(InvalidInput, path.suffix("/payload"), "", "tool result payload is missing")
	}
}

func (check *capabilityCheck) cachePath(intents []CacheIntent, path diagnosticPath) {
	for index, intent := range intents {
		location := path.index(index)
		switch intent.Kind {
		case "breakpoint":
			check.requirePath(CacheBreakpointsCapability, location)
			if intent.Value.IsNull() {
				if !intent.TTL.IsZero() {
					check.addPath(InvalidInput, location.suffix("/ttl"), CacheBreakpointsCapability, "a null policy cannot contain TTL")
				}
			} else if policy, err := intent.Value.ReadObject(); err != nil {
				check.addPath(InvalidInput, location.suffix("/value"), CacheBreakpointsCapability, "cache policy must be an object or null")
			} else if !policy["ttl"].IsZero() {
				check.addPath(InvalidInput, location.suffix("/value/ttl"), CacheBreakpointsCapability, "move ttl to the cache intent's ttl field")
			}
		case "key":
			check.requirePath(CacheKeysCapability, location)
		case "retention":
			check.requirePath(CacheRetentionCapability, location)
		case "resource":
			check.requirePath(CacheResourcesCapability, location)
			if intent.Resource == nil {
				check.addPath(InvalidInput, location, CacheResourcesCapability, "cache reference is missing")
			} else {
				check.resourcesPath([]Resource{*intent.Resource}, location.suffix("/resource"))
			}
		case "mode":
			check.requirePath(CacheOptionsCapability, location)
			var mode string
			if err := intent.Value.Decode(&mode); err != nil || (mode != "explicit" && mode != "implicit") {
				check.addPath(InvalidInput, location.suffix("/value"), CacheOptionsCapability, "cache mode must be explicit or implicit")
			}
		case "options.ttl":
			check.requirePath(CacheOptionsCapability, location)
			var ttl string
			if err := intent.Value.Decode(&ttl); err != nil || ttl == "" {
				check.addPath(InvalidInput, location.suffix("/value"), CacheOptionsCapability, "minimum cache lifetime must be a nonempty string")
			}
		case "prewarm":
			check.requirePath(CachePrewarmCapability, location)
		default:
			check.addPath(InvalidInput, location.suffix("/kind"), "", "unknown cache intent")
		}
	}
}

func (check *capabilityCheck) resourcesPath(resources []Resource, path diagnosticPath) {
	for index, resource := range resources {
		location := path.index(index)
		if resource.ID.IsZero() || resource.ID.IsNull() || resource.Scope.Provider == "" || resource.Scope.Account == "" {
			check.addPath(InvalidInput, location, "", "resource requires an identity and provider/account provenance")
		}
		if !CheckScope(resource.Scope, check.target.Scope) {
			check.addPath(ResourceScopeMismatch, location, "", "resource provider/account/model/session differs from target")
		}
		switch resource.Kind {
		case "signature":
			check.requirePath(SignaturesCapability, location)
		case "encrypted_content":
			check.requirePath(EncryptedReasoningCapability, location)
		case "session":
			check.requirePath(SessionsCapability, location)
		case "file", "file_id":
			check.requirePath(DocumentsCapability, location)
		case "audio", "container":
			if resource.Kind == "audio" {
				check.requirePath(AudioCapability, location)
			} else {
				check.requirePath(ServerToolsCapability, location)
			}
		case "cache":
			check.requirePath(CacheResourcesCapability, location)
		default:
			check.addPath(UnsupportedNative, location, NativeExtensionsCapability, "resource kind has no declared mapping")
		}
	}
}

func (check *capabilityCheck) metadataSnapshot(metadata []ResponseMetadata, path string) {
	for index, field := range metadata {
		check.metadataValue(field, fmt.Sprintf("%s/%d", path, index))
		if field.Update != "" {
			check.add(InvalidInput, fmt.Sprintf("%s/%d/update", path, index), "", "metadata snapshots cannot carry update instructions")
		}
	}
}

func (check *capabilityCheck) metadataValue(field ResponseMetadata, path string) {
	if err := validateCoreMetadataValue(field, path); err != nil {
		for _, issue := range err.(*ConversionError).Issues {
			check.add(issue.Code, issue.Path, issue.Capability, issue.Reason)
		}
	}
}

func (check *capabilityCheck) addPath(code IssueCode, path diagnosticPath, capability Capability, reason string) {
	check.add(code, path.String(), capability, reason)
}
func (check *capabilityCheck) requirePath(capability Capability, path diagnosticPath) {
	if !check.target.Capabilities[capability] {
		check.addPath(UnsupportedCapability, path, capability, "target does not declare this capability")
	}
}
func (check *capabilityCheck) requireNamePath(value Value, path diagnosticPath) {
	var name string
	if err := value.Decode(&name); err != nil || name == "" || value.IsNull() {
		check.addPath(InvalidInput, path, "", "a nonempty string is required")
	}
}
func (check *capabilityCheck) nativePath(native *Native, path diagnosticPath) {
	if native == nil || !CanPreserveNative(native.Source, check.target) {
		check.addPath(UnsupportedNative, path, NativeExtensionsCapability, "opaque content lacks compatible native provenance")
		return
	}
	if !CheckScope(native.Source.Scope, check.target.Scope) {
		check.addPath(ResourceScopeMismatch, path, "", "native scope differs from target")
	}
}
func (check *capabilityCheck) metadataSnapshotPath(metadata []ResponseMetadata, path diagnosticPath) {
	for index, field := range metadata {
		location := path.index(index)
		// On failure re-run with the real path: nested validators own their
		// precise path overrides and wording. Success never formats a path.
		if validateCoreMetadataValue(field, "") != nil {
			check.metadataValue(field, location.String())
		}
		if field.Update != "" {
			check.addPath(InvalidInput, location.suffix("/update"), "", "metadata snapshots cannot carry update instructions")
		}
	}
}

func (check *capabilityCheck) content(nodes []Node, path string, depth int, isServerArtifact bool) {
	var buffer [256]byte
	lazy := append(diagnosticPath(buffer[:0]), path...)
	check.contentPath(nodes, lazy, depth, isServerArtifact)
}

func (check *capabilityCheck) call(node Node, path string) {
	var buffer [256]byte
	lazy := append(diagnosticPath(buffer[:0]), path...)
	check.callPath(node, lazy)
}

func (check *capabilityCheck) result(node Node, path string) {
	var buffer [256]byte
	lazy := append(diagnosticPath(buffer[:0]), path...)
	check.resultPath(node, lazy)
}

func (check *capabilityCheck) cache(intents []CacheIntent, path string) {
	var buffer [256]byte
	lazy := append(diagnosticPath(buffer[:0]), path...)
	check.cachePath(intents, lazy)
}

func (check *capabilityCheck) resources(resources []Resource, path string) {
	var buffer [256]byte
	lazy := append(diagnosticPath(buffer[:0]), path...)
	check.resourcesPath(resources, lazy)
}
