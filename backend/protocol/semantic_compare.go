package protocol

// comparableSemantic removes adapter bookkeeping, never user payload fields.
// Native wire preservation is asserted separately against the original bytes.
func comparableSemantic(input any) (Value, error) {
	switch value := input.(type) {
	case *Request:
		copy := *value
		copy.Source, copy.SchemaVersion, copy.Native = Identity{}, 0, nil
		copy.Content = comparableNodes(copy.Content)
		copy.Tools = append([]Tool(nil), value.Tools...)
		for index := range copy.Tools {
			copy.Tools[index].Native = nil
		}
		return EncodeValue(copy)
	case *Response:
		copy := *value
		copy.Source, copy.SchemaVersion, copy.Native = Identity{}, 0, nil
		copy.Content = comparableNodes(copy.Content)
		return EncodeValue(copy)
	case Event:
		value.Source, value.SchemaVersion, value.Native = Identity{}, 0, nil
		if value.Item != nil {
			item := comparableNodes([]Node{*value.Item})[0]
			value.Item = &item
		}
		if value.Response != nil {
			response := *value.Response
			response.Source, response.SchemaVersion, response.Native = Identity{}, 0, nil
			response.Content = comparableNodes(response.Content)
			value.Response = &response
		}
		if value.Request != nil {
			request := *value.Request
			request.Source, request.SchemaVersion, request.Native = Identity{}, 0, nil
			request.Content = comparableNodes(request.Content)
			request.Tools = append([]Tool(nil), request.Tools...)
			for index := range request.Tools {
				request.Tools[index].Native = nil
			}
			value.Request = &request
		}
		return EncodeValue(value)
	case []Event:
		items := make([]Value, len(value))
		for index, event := range value {
			converted, err := comparableSemantic(event)
			if err != nil {
				return Value{}, err
			}
			items[index] = converted
		}
		return EncodeValue(items)
	default:
		return EncodeValue(input)
	}
}

func comparableNodes(nodes []Node) []Node {
	copy := append([]Node(nil), nodes...)
	for index := range copy {
		copy[index].Native = nil
		copy[index].Children = comparableNodes(copy[index].Children)
	}
	return copy
}

type capabilityEvidence struct {
	observed             CapabilitySet
	hasFunctionLifecycle bool
	hasFreeTextLifecycle bool
}

func observeCapabilities(value any) capabilityEvidence {
	evidence := capabilityEvidence{observed: CapabilitySet{}}
	switch semantic := value.(type) {
	case *Request:
		evidence.nodes(semantic.Content)
		evidence.cache(semantic.Cache)
		evidence.resources(semantic.Resources)
		definitions := map[string]ToolKind{}
		for _, tool := range semantic.Tools {
			switch tool.Kind {
			case FunctionTool:
				evidence.observed[FunctionToolsCapability] = true
			case FreeTextTool:
				evidence.observed[FreeTextToolsCapability] = true
			case ServerTool:
				evidence.observed[ServerToolsCapability] = true
			case OpaqueTool:
				evidence.observed[NativeExtensionsCapability] = true
			}
			if !tool.Name.IsZero() {
				definitions[tool.Name.raw] = tool.Kind
			}
			evidence.cache(tool.Cache)
		}
		calls := map[string]InputKind{}
		var visit func([]Node)
		visit = func(nodes []Node) {
			for _, node := range nodes {
				if node.Kind == ToolCallNode && node.Input != nil {
					kind := definitions[node.Name.raw]
					if (kind == FunctionTool && node.Input.Kind == JSONInput) || (kind == FreeTextTool && node.Input.Kind == TextInput) {
						calls[node.CallID.raw] = node.Input.Kind
					}
				}
				if node.Kind == ToolResultNode {
					switch calls[node.CallID.raw] {
					case JSONInput:
						evidence.hasFunctionLifecycle = true
					case TextInput:
						evidence.hasFreeTextLifecycle = true
					}
				}
				visit(node.Children)
			}
		}
		visit(semantic.Content)
	case *Response:
		evidence.nodes(semantic.Content)
		if semantic.Usage != nil {
			evidence.observed[UsageCapability] = true
		}
	case []Event:
		for _, event := range semantic {
			if event.Request != nil {
				requestEvidence := observeCapabilities(event.Request)
				for capability, supported := range requestEvidence.observed {
					evidence.observed[capability] = supported
				}
			}
			if event.Item != nil {
				evidence.nodes([]Node{*event.Item})
			}
			if event.Response != nil {
				evidence.nodes(event.Response.Content)
			}
			if event.Usage != nil || (event.Response != nil && event.Response.Usage != nil) {
				evidence.observed[UsageCapability] = true
			}
			if event.Media != nil {
				evidence.observed[RealtimeMediaCapability] = true
			}
			if isSessionControl(event.Type) {
				evidence.observed[SessionsCapability] = true
			}
			if event.Type == NativeEvent {
				evidence.observed[NativeExtensionsCapability] = true
			}
		}
	}
	return evidence
}

func (evidence *capabilityEvidence) nodes(nodes []Node) {
	for _, node := range nodes {
		switch node.Kind {
		case TextNode, RefusalNode:
			evidence.observed[TextCapability] = true
		case ImageNode:
			evidence.observed[ImagesCapability] = true
		case AudioNode:
			evidence.observed[AudioCapability] = true
		case VideoNode:
			evidence.observed[VideoCapability] = true
		case DocumentNode:
			evidence.observed[DocumentsCapability] = true
		case ReasoningNode:
			evidence.observed[ReasoningCapability] = true
		case OpaqueNode:
			evidence.observed[NativeExtensionsCapability] = true
		case ToolCallNode:
			if node.Input != nil && node.Input.Kind == JSONInput {
				evidence.observed[FunctionToolsCapability] = true
			}
			if node.Input != nil && node.Input.Kind == TextInput {
				evidence.observed[FreeTextToolsCapability] = true
			}
		}
		evidence.cache(node.Cache)
		evidence.resources(node.Resources)
		evidence.nodes(node.Children)
	}
}

func (evidence *capabilityEvidence) cache(intents []CacheIntent) {
	for _, intent := range intents {
		switch intent.Kind {
		case "breakpoint":
			evidence.observed[CacheBreakpointsCapability] = true
		case "key":
			evidence.observed[CacheKeysCapability] = true
		case "retention":
			evidence.observed[CacheRetentionCapability] = true
		case "resource":
			evidence.observed[CacheResourcesCapability] = true
		}
	}
}

func (evidence *capabilityEvidence) resources(resources []Resource) {
	for _, resource := range resources {
		switch resource.Kind {
		case "signature":
			evidence.observed[SignaturesCapability] = true
		case "encrypted_content":
			evidence.observed[EncryptedReasoningCapability] = true
		case "session":
			evidence.observed[SessionsCapability] = true
		case "cache":
			evidence.observed[CacheResourcesCapability] = true
		case "file", "file_id":
			evidence.observed[DocumentsCapability] = true
		}
	}
}
