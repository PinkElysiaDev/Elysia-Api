package protocol

import "fmt"

// Capability is an independently verifiable semantic feature.
type Capability string

const (
	TextCapability               Capability = "text"
	FunctionToolsCapability      Capability = "tools.function"
	FreeTextToolsCapability      Capability = "tools.free_text"
	ServerToolsCapability        Capability = "tools.server"
	NativeExtensionsCapability   Capability = "native_extensions"
	ImagesCapability             Capability = "images"
	AudioCapability              Capability = "audio"
	VideoCapability              Capability = "video"
	DocumentsCapability          Capability = "documents"
	ReasoningCapability          Capability = "reasoning"
	SignaturesCapability         Capability = "reasoning.signatures"
	EncryptedReasoningCapability Capability = "reasoning.encrypted"
	CacheBreakpointsCapability   Capability = "cache.breakpoints"
	CacheKeysCapability          Capability = "cache.keys"
	CacheRetentionCapability     Capability = "cache.retention"
	CacheResourcesCapability     Capability = "cache.resources"
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
	check := capabilityCheck{target: target, limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	if err := limits.Validate(); err != nil {
		check.add(InvalidDefinition, "", "", err.Error())
		return check.issues
	}
	if request == nil || request.SchemaVersion != SemanticSchemaVersion {
		check.add(InvalidInput, "/schemaVersion", "", "missing request or unsupported semantic contract version")
		return check.issues
	}
	for index, tool := range request.Tools {
		path := fmt.Sprintf("/tools/%d", index)
		switch tool.Kind {
		case FunctionTool:
			check.require(FunctionToolsCapability, path)
			check.requireName(tool.Name, path+"/name")
		case FreeTextTool:
			check.require(FreeTextToolsCapability, path)
			check.requireName(tool.Name, path+"/name")
		case ServerTool:
			check.require(ServerToolsCapability, path)
			check.native(tool.Native, path)
		case OpaqueTool:
			check.require(NativeExtensionsCapability, path)
			check.native(tool.Native, path)
		default:
			check.add(InvalidInput, path+"/kind", "", "unknown tool kind")
		}
		check.cache(tool.Cache, path+"/cache")
	}
	check.content(request.Content, "/content", 1)
	check.cache(request.Cache, "/cache")
	check.resources(request.Resources, "/resources")
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

func (check *capabilityCheck) content(nodes []Node, path string, depth int) {
	if depth > check.limits.Depth {
		check.add(LimitExceeded, path, "", "content nesting exceeds configured limit")
		return
	}
	for index, node := range nodes {
		check.nodes++
		if check.nodes > check.limits.Nodes {
			check.add(LimitExceeded, path, "", "content node count exceeds configured limit")
			return
		}
		location := fmt.Sprintf("%s/%d", path, index)
		switch node.Kind {
		case MessageNode:
			check.requireName(node.Role, location+"/role")
		case TextNode, RefusalNode:
			check.require(TextCapability, location)
		case ImageNode:
			check.require(ImagesCapability, location)
		case AudioNode:
			check.require(AudioCapability, location)
		case VideoNode:
			check.require(VideoCapability, location)
		case DocumentNode:
			check.require(DocumentsCapability, location)
		case ReasoningNode:
			check.require(ReasoningCapability, location)
			if node.ReasoningForm != "" && node.ReasoningForm != SummaryReasoning && node.ReasoningForm != StructuredReasoning {
				check.add(InvalidInput, location+"/reasoningForm", ReasoningCapability, "unknown reasoning representation")
			}
			if node.ReasoningForm != "" && !node.Payload.IsZero() {
				check.add(InvalidInput, location+"/payload", ReasoningCapability, "reasoning summary text belongs to ordered children")
			}
			if node.ReasoningForm == SummaryReasoning || node.ReasoningForm == StructuredReasoning {
				for i, child := range node.Children {
					if child.Kind != TextNode {
						check.add(InvalidInput, fmt.Sprintf("%s/children/%d/kind", location, i), ReasoningCapability, "reasoning summary parts must be text")
					}
				}
			}
		case ToolCallNode:
			check.call(node, location)
		case ToolResultNode:
			check.result(node, location)
		case OpaqueNode:
			check.require(NativeExtensionsCapability, location)
			check.native(node.Native, location)
		default:
			check.add(InvalidInput, location+"/kind", "", "unknown semantic content kind")
		}
		check.cache(node.Cache, location+"/cache")
		check.resources(node.Resources, location+"/resources")
		if node.ReasoningContent != nil {
			if node.Kind != ReasoningNode || node.ReasoningForm != StructuredReasoning {
				check.add(InvalidInput, location+"/reasoningContent", ReasoningCapability, "reasoning content requires structured reasoning")
			}
			for i, part := range node.ReasoningContent {
				var text string
				if part.Kind != TextNode || part.Payload.IsZero() || part.Payload.IsNull() || part.Payload.Decode(&text) != nil {
					check.add(InvalidInput, fmt.Sprintf("%s/reasoningContent/%d", location, i), ReasoningCapability, "visible reasoning parts must contain string text")
				}
			}
			check.content(node.ReasoningContent, location+"/reasoningContent", depth+1)
		}
		if len(node.Children) > 0 {
			check.content(node.Children, location+"/children", depth+1)
		}
	}
}

func (check *capabilityCheck) call(node Node, path string) {
	check.requireName(node.Name, path+"/name")
	check.requireName(node.CallID, path+"/callId")
	var id string
	if err := node.CallID.Decode(&id); err == nil && id != "" {
		if _, hasCall := check.calls[id]; hasCall {
			check.add(InvalidAssociation, path+"/callId", "", "call identity was already used")
		}
		check.calls[id] = struct{}{}
	}
	if node.Input == nil || node.Input.Value.IsZero() {
		check.add(InvalidInput, path+"/input", "", "tool input is missing")
		return
	}
	switch node.Input.Kind {
	case JSONInput:
		check.require(FunctionToolsCapability, path)
	case TextInput:
		check.require(FreeTextToolsCapability, path)
		var text string
		if err := node.Input.Value.Decode(&text); err != nil || node.Input.Value.IsNull() {
			check.add(InvalidInput, path+"/input/value", "", "free text input must be a string")
		}
	default:
		check.add(InvalidInput, path+"/input/kind", "", "unknown tool input kind")
	}
}

func (check *capabilityCheck) result(node Node, path string) {
	check.requireName(node.CallID, path+"/callId")
	var id string
	if err := node.CallID.Decode(&id); err != nil || id == "" {
		return
	}
	if _, hasCall := check.calls[id]; !hasCall {
		check.add(InvalidAssociation, path+"/callId", "", "result has no preceding call in this history")
	}
	if _, hasResult := check.results[id]; hasResult {
		check.add(InvalidAssociation, path+"/callId", "", "call already has a result")
	}
	check.results[id] = struct{}{}
	if node.Payload.IsZero() {
		check.add(InvalidInput, path+"/payload", "", "tool result payload is missing")
	}
}

func (check *capabilityCheck) cache(intents []CacheIntent, path string) {
	for index, intent := range intents {
		location := fmt.Sprintf("%s/%d", path, index)
		switch intent.Kind {
		case "breakpoint":
			check.require(CacheBreakpointsCapability, location)
			if intent.Value.IsNull() {
				if !intent.TTL.IsZero() {
					check.add(InvalidInput, location+"/ttl", CacheBreakpointsCapability, "a null policy cannot contain TTL")
				}
			} else if policy, err := intent.Value.ReadObject(); err != nil {
				check.add(InvalidInput, location+"/value", CacheBreakpointsCapability, "cache policy must be an object or null")
			} else if !policy["ttl"].IsZero() {
				check.add(InvalidInput, location+"/value/ttl", CacheBreakpointsCapability, "move ttl to the cache intent's ttl field")
			}
		case "key":
			check.require(CacheKeysCapability, location)
		case "retention":
			check.require(CacheRetentionCapability, location)
		case "resource":
			check.require(CacheResourcesCapability, location)
			if intent.Resource == nil {
				check.add(InvalidInput, location, CacheResourcesCapability, "cache reference is missing")
			} else {
				check.resources([]Resource{*intent.Resource}, location+"/resource")
			}
		case "mode":
			check.require(CacheOptionsCapability, location)
			var mode string
			if err := intent.Value.Decode(&mode); err != nil || (mode != "explicit" && mode != "implicit") {
				check.add(InvalidInput, location+"/value", CacheOptionsCapability, "cache mode must be explicit or implicit")
			}
		case "options.ttl":
			check.require(CacheOptionsCapability, location)
			var ttl string
			if err := intent.Value.Decode(&ttl); err != nil || ttl == "" {
				check.add(InvalidInput, location+"/value", CacheOptionsCapability, "minimum cache lifetime must be a nonempty string")
			}
		case "prewarm":
			check.require(CachePrewarmCapability, location)
		default:
			check.add(InvalidInput, location+"/kind", "", "unknown cache intent")
		}
	}
}

func (check *capabilityCheck) resources(resources []Resource, path string) {
	for index, resource := range resources {
		location := fmt.Sprintf("%s/%d", path, index)
		if resource.ID.IsZero() || resource.ID.IsNull() || resource.Scope.Provider == "" || resource.Scope.Account == "" {
			check.add(InvalidInput, location, "", "resource requires an identity and provider/account provenance")
		}
		if !CheckScope(resource.Scope, check.target.Scope) {
			check.add(ResourceScopeMismatch, location, "", "resource provider/account/model/session differs from target")
		}
		switch resource.Kind {
		case "signature":
			check.require(SignaturesCapability, location)
		case "encrypted_content":
			check.require(EncryptedReasoningCapability, location)
		case "session":
			check.require(SessionsCapability, location)
		case "file", "file_id":
			check.require(DocumentsCapability, location)
		case "cache":
			check.require(CacheResourcesCapability, location)
		default:
			check.add(UnsupportedNative, location, NativeExtensionsCapability, "resource kind has no declared mapping")
		}
	}
}
