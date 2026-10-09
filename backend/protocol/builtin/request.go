package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

const maxBuiltinChoices = 1

var parameterFields = map[string]map[string]string{
	Chat:      {"max_completion_tokens": "max_output_tokens", "temperature": "temperature", "top_p": "top_p", "stop": "stop", "stream": "stream", "parallel_tool_calls": "parallel_tool_calls", "seed": "seed", "frequency_penalty": "frequency_penalty", "presence_penalty": "presence_penalty", "metadata": "metadata", "store": "store", "user": "user", "response_format": "response_format", "reasoning_effort": "reasoning_effort", "n": "n", "logprobs": "logprobs", "top_logprobs": "top_logprobs", "logit_bias": "logit_bias"},
	Responses: {"max_output_tokens": "max_output_tokens", "temperature": "temperature", "top_p": "top_p", "stream": "stream", "parallel_tool_calls": "parallel_tool_calls", "metadata": "metadata", "store": "store", "user": "user", "reasoning": "responses_reasoning", "text": "responses_text", "include": "responses_include", "previous_response_id": "responses_previous_response_id", "truncation": "responses_truncation"},
	Anthropic: {"max_tokens": "max_output_tokens", "temperature": "temperature", "top_p": "top_p", "top_k": "top_k", "stop_sequences": "stop", "stream": "stream", "metadata": "anthropic_metadata", "thinking": "anthropic_thinking", "output_config": "anthropic_output_config"},
	Gemini:    {"maxOutputTokens": "max_output_tokens", "temperature": "temperature", "topP": "top_p", "topK": "top_k", "stopSequences": "stop", "seed": "seed", "candidateCount": "n", "responseMimeType": "gemini_response_mime", "responseSchema": "gemini_response_schema", "thinkingConfig": "gemini_thinking"},
}

func (adapter module) decodeRequest(input p.Value, options p.EvaluationContext) (*p.Request, error) {
	fields, err := input.ReadObject()
	if err != nil {
		return nil, err
	}
	request := &p.Request{SchemaVersion: p.SemanticSchemaVersion, Source: options.Identity(), Model: fields["model"], Parameters: p.Object{}, Content: []p.Node{}}
	history := &historyState{calls: map[string][]p.Value{}}
	known := []string{"model", "tools", "tool_choice", "cache_control", "prompt_cache_key", "prompt_cache_retention", "prompt_cache_options", "prewarm"}
	parameterInput := fields
	if adapter.name == Gemini {
		request.Model = p.StringValue(options.Scope.Model)
		if model := options.Values["model"]; !model.IsZero() {
			request.Model = model
		}
		parameterInput = p.Object{}
		if config := fields["generationConfig"]; !config.IsZero() {
			parameterInput, err = config.ReadObject()
			if err != nil {
				return nil, err
			}
		}
		known = append(known, "generationConfig", "cachedContent", "systemInstruction", "contents", "toolConfig")
	}
	for wire, semantic := range parameterFields[adapter.name] {
		if value, exists := parameterInput[wire]; exists {
			request.Parameters[semantic] = value
		}
		if adapter.name != Gemini {
			known = append(known, wire)
		}
	}
	if adapter.name == Chat {
		known = append(known, "max_tokens", "messages", "functions", "function_call", "stream_options")
		if options.DefinitionRequires("conversion.client_output.v1") {
			request.ClientOutput, err = p.ParseClientOutput(fields["stream_options"])
			if err != nil {
				return nil, err
			}
		} else if value, exists := fields["stream_options"]; exists {
			request.Parameters["stream_options"] = value
		}
		if value, exists := fields["max_tokens"]; exists {
			if _, ambiguous := fields["max_completion_tokens"]; ambiguous {
				return nil, fmt.Errorf("max_tokens and max_completion_tokens cannot both be present")
			}
			request.Parameters["max_output_tokens"] = value
		}
	}
	if adapter.name == Responses {
		if err := p.ValidateResponsesContext(fields["truncation"], fields["previous_response_id"]); err != nil {
			return nil, err
		}
		if _, err := p.ParseResponsesStore(fields["store"]); err != nil {
			return nil, err
		}
		if _, err := p.ParseResponsesInclude(fields["include"]); err != nil {
			return nil, err
		}
		known = append(known, "input", "instructions")
		if previous := fields["previous_response_id"]; !previous.IsZero() && !previous.IsNull() {
			delete(request.Parameters, "responses_previous_response_id")
			request.Resources = append(request.Resources, p.Resource{Kind: "session", ID: previous, Scope: options.Scope})
		}
	}
	if adapter.name == Anthropic {
		known = append(known, "messages", "system")
	}
	for key, value := range adapter.extensions(fields, known) {
		request.Parameters[key] = value
	}
	if adapter.name == Gemini {
		keys := []string{}
		for wire := range parameterFields[Gemini] {
			keys = append(keys, wire)
		}
		if extra := collectUnknown(parameterInput, keys); !extra.IsZero() {
			request.Parameters["gemini_generation_extensions"] = extra
		}
	}
	request.Cache, err = decodeCache(fields, "request")
	if err != nil {
		return nil, err
	}
	for _, entry := range []struct{ field, kind string }{{"prompt_cache_key", "key"}, {"prompt_cache_retention", "retention"}} {
		if value := fields[entry.field]; !value.IsZero() {
			request.Cache = append(request.Cache, p.CacheIntent{Kind: entry.kind, Location: "request", Value: value})
		}
	}
	// The mode and lifetime settings share one wire object but stay independent
	// intents: implicit/explicit selects behaviour, while ttl sets a minimum
	// lifetime. Neither is the maximum-retention setting, so they must not be
	// folded into the retention intent even when they travel together.
	if value := fields["prompt_cache_options"]; !value.IsZero() && !value.IsNull() {
		options, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		for _, entry := range []struct{ field, kind string }{{"mode", "mode"}, {"ttl", "options.ttl"}} {
			if setting := options[entry.field]; !setting.IsZero() {
				request.Cache = append(request.Cache, p.CacheIntent{Kind: entry.kind, Location: "request", Value: setting})
			}
		}
	}
	if value := fields["prewarm"]; !value.IsZero() {
		request.Cache = append(request.Cache, p.CacheIntent{Kind: "prewarm", Location: "request", Value: value})
	}
	if value := fields["cachedContent"]; !value.IsZero() {
		request.Cache = append(request.Cache, p.CacheIntent{Kind: "resource", Location: "request", Resource: &p.Resource{Kind: "cache", ID: value, Scope: options.Scope}})
	}
	system, content, path := p.Value{}, fields["messages"], "/messages"
	switch adapter.name {
	case Responses:
		system, content, path = fields["instructions"], fields["input"], "/input"
	case Anthropic:
		system = fields["system"]
	case Gemini:
		content, path = fields["contents"], "/contents"
		if value := fields["systemInstruction"]; !value.IsZero() {
			block, err := value.ReadObject()
			if err != nil {
				return nil, err
			}
			system = block["parts"]
			if extra := collectUnknown(block, []string{"parts"}); !extra.IsZero() {
				key := wireExtensionPrefix + adapter.family
				root := p.Object{}
				if value := request.Parameters[key]; !value.IsZero() {
					root, _ = value.ReadObject()
				}
				root["systemInstruction"] = extra
				request.Parameters[key] = object(root)
			}
		}
	}
	if !system.IsZero() {
		children, err := adapter.decodeContent(system, "/system", p.DecodeRequest, options, history)
		if err != nil {
			return nil, err
		}
		node := p.Node{Kind: p.MessageNode, Role: p.StringValue("system"), Children: children}
		if adapter.name == Responses {
			node.Native = adapter.native(system, "/instructions", p.DecodeRequest, options)
		}
		request.Content = append(request.Content, node)
	}
	if adapter.name == Responses && !content.IsZero() && !content.IsNull() && !strings.HasPrefix(strings.TrimSpace(string(content.Bytes())), "[") {
		text, err := stringValue(content)
		if err != nil {
			return nil, err
		}
		request.Content = append(request.Content, p.Node{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(text)}}})
	} else {
		nodes, err := adapter.decodeMessages(content, path, p.DecodeRequest, options, history)
		if err != nil {
			return nil, err
		}
		request.Content = append(request.Content, nodes...)
	}
	tools := fields["tools"]
	if functions := fields["functions"]; adapter.name == Chat && !functions.IsZero() {
		if !tools.IsZero() {
			return nil, fmt.Errorf("tools and legacy functions cannot both be present")
		}
		functions, err := readArray(functions)
		if err != nil {
			return nil, err
		}
		var entries []p.Value
		for _, function := range functions {
			entries = append(entries, object(p.Object{"type": p.StringValue("function"), "function": function}))
		}
		tools = array(entries)
	}
	request.Tools, err = adapter.decodeTools(tools, options)
	if err != nil {
		return nil, err
	}
	choice := fields["tool_choice"]
	if adapter.name == Gemini {
		choice = fields["toolConfig"]
	}
	if choice.IsZero() && adapter.name == Chat {
		choice = fields["function_call"]
	}
	request.ToolChoice, err = adapter.decodeChoice(choice)
	if err != nil {
		return nil, err
	}
	associateResultNames(request.Content, map[string]p.Value{})
	if err := checkRequestChoices(request); err != nil {
		return nil, err
	}
	return request, nil
}

func checkRequestChoices(request *p.Request) error {
	value := request.Parameters["n"]
	if value.IsZero() || value.IsNull() {
		return nil
	}
	var count int
	if err := value.Decode(&count); err != nil || count < 1 {
		return fmt.Errorf("choice count must be a positive integer")
	}
	if count > maxBuiltinChoices {
		return unsupported("/parameters/n", "multiple choices require an adapter with an explicit choice lifecycle")
	}
	return nil
}

func associateResultNames(nodes []p.Node, names map[string]p.Value) {
	for index := range nodes {
		node := &nodes[index]
		if node.Kind == p.ToolCallNode {
			names[string(node.CallID.Bytes())] = node.Name
		}
		if node.Kind == p.ToolResultNode && node.Name.IsZero() {
			node.Name = names[string(node.CallID.Bytes())]
		}
		associateResultNames(node.Children, names)
	}
}

func (adapter module) encodeRequest(request *p.Request, options p.EvaluationContext) (p.Value, error) {
	if err := checkRequestChoices(request); err != nil {
		return p.Value{}, err
	}
	fields := p.Object{"model": request.Model}
	if adapter.name == Gemini {
		delete(fields, "model")
	}
	if err := adapter.encodeParameters(fields, request.Parameters); err != nil {
		return p.Value{}, err
	}
	if adapter.name == Chat && request.ClientOutput != nil {
		if raw := request.ClientOutput.RawStreamOptions; !raw.IsZero() {
			fields["stream_options"] = raw
		}
		var stream bool
		_ = request.Parameters["stream"].Decode(&stream)
		if stream && request.ClientOutput.CollectUsage {
			options := p.Object{}
			if fields["stream_options"].IsObject() {
				options, _ = fields["stream_options"].ReadObject()
			}
			options["include_usage"], _ = p.EncodeValue(true)
			fields["stream_options"], _ = p.EncodeValue(options)
		}
	}
	nodes := request.Content
	if adapter.name == Responses && len(nodes) > 0 && hasNativeInstructions(nodes[0], options) {
		instructions, err := encodeNativeInstructions(nodes[0])
		if err != nil {
			return p.Value{}, err
		}
		fields["instructions"] = instructions
		nodes = nodes[1:]
	}
	content, system, err := adapter.encodeMessages(nodes, p.EncodeRequest, options)
	if err != nil {
		return p.Value{}, err
	}
	switch adapter.name {
	case Chat:
		fields["messages"] = content
	case Responses:
		fields["input"] = content
	case Anthropic:
		fields["messages"], fields["system"] = content, system
	case Gemini:
		fields["contents"], fields["systemInstruction"] = content, system
	}
	fields["tools"], err = adapter.encodeTools(request.Tools, options)
	if err != nil {
		return p.Value{}, err
	}
	choice, err := adapter.encodeChoice(request.ToolChoice)
	if err != nil {
		return p.Value{}, err
	}
	if adapter.name == Gemini {
		fields["toolConfig"] = choice
	} else {
		fields["tool_choice"] = choice
	}
	if err := adapter.encodeCacheIntents(fields, request.Cache); err != nil {
		return p.Value{}, err
	}
	if adapter.name == Anthropic {
		// Non-blocking: an off-order TTL is reported through the diagnostic sink,
		// never as a wire error (see warnBreakpointOrder).
		adapter.warnBreakpointOrder(request, options.Diagnostics)
	}
	for _, resource := range request.Resources {
		if adapter.name != Responses || resource.Kind != "session" || request.Source.Family != options.Identity().Family || request.Source.WireVersion != options.Identity().WireVersion {
			return p.Value{}, unsupported("/resources", "request references require their original protocol and an equivalent resource field")
		}
		if !fields["previous_response_id"].IsZero() {
			return p.Value{}, unsupported("/resources", "Responses accepts one preceding response reference")
		}
		fields["previous_response_id"] = resource.ID
	}
	if err := adapter.preserveExtensions(fields, request.Parameters); err != nil {
		return p.Value{}, err
	}
	return object(fields), nil
}

func hasNativeInstructions(node p.Node, options p.EvaluationContext) bool {
	return node.Native != nil && node.Native.Source.Path == "/instructions" &&
		p.CanPreserveNative(node.Native.Source, p.Target{Protocol: options.Identity(), Direction: p.EncodeRequest})
}

func encodeNativeInstructions(node p.Node) (p.Value, error) {
	if node.Kind != p.MessageNode || node.Role != p.StringValue("system") || hasStringMetadata(node) || !node.Payload.IsZero() {
		return p.Value{}, unsupported("/instructions", "modified instructions cannot be represented as a native system string")
	}
	if len(node.Children) == 0 && node.Native.Value.IsNull() {
		return node.Native.Value, nil
	}
	if len(node.Children) != 1 {
		return p.Value{}, unsupported("/instructions", "native instructions require one text block")
	}
	text := node.Children[0]
	if text.Kind != p.TextNode || hasStringMetadata(text) || !text.Role.IsZero() || len(text.Children) > 0 {
		return p.Value{}, unsupported("/instructions", "native instructions cannot carry block metadata")
	}
	return text.Payload, nil
}

// A native string has no location for semantic identity or block metadata.
func hasStringMetadata(node p.Node) bool {
	return !node.ID.IsZero() || !node.Status.IsZero() || !node.Name.IsZero() || !node.CallID.IsZero() ||
		node.Input != nil || node.ReasoningForm != "" || len(node.Cache) > 0 || len(node.Attributes) > 0 || len(node.Resources) > 0
}

// encodeCacheIntents 把语义缓存意图逐条落到目标 wire 字段；一个 wire 字段
// 只承载一个策略，重复即显式报错。
func (adapter module) encodeCacheIntents(fields p.Object, intents []p.CacheIntent) error {
	for _, intent := range intents {
		switch intent.Kind {
		case "breakpoint":
			if !fields["cache_control"].IsZero() {
				return unsupported("/cache", "one wire cache_control cannot express multiple policies")
			}
			if err := adapter.encodeCache(fields, []p.CacheIntent{intent}); err != nil {
				return err
			}
		case "key", "retention":
			if adapter.name != Chat && adapter.name != Responses {
				return unsupported("/cache", "target has no cache key/retention mapping")
			}
			key := "prompt_cache_key"
			if intent.Kind == "retention" {
				key = "prompt_cache_retention"
			}
			if !fields[key].IsZero() {
				return unsupported("/cache", "one wire field cannot express multiple "+intent.Kind+" intents")
			}
			fields[key] = intent.Value
		case "resource":
			if adapter.name != Gemini {
				return unsupported("/cache", "target has no explicit cache resource reference")
			}
			if !fields["cachedContent"].IsZero() {
				return unsupported("/cache", "one wire field cannot express multiple resource intents")
			}
			fields["cachedContent"] = intent.Resource.ID
		case "mode", "options.ttl":
			if adapter.name != Chat && adapter.name != Responses {
				return unsupported("/cache", "target has no cache options mapping")
			}
			cacheOptions := p.Object{}
			if existing := fields["prompt_cache_options"]; !existing.IsZero() {
				parsed, err := existing.ReadObject()
				if err != nil {
					return err
				}
				cacheOptions = parsed
			}
			key := "mode"
			if intent.Kind == "options.ttl" {
				key = "ttl"
			}
			if !cacheOptions[key].IsZero() {
				return unsupported("/cache", "one wire field cannot express multiple "+intent.Kind+" intents")
			}
			cacheOptions[key] = intent.Value
			fields["prompt_cache_options"] = object(cacheOptions)
		case "prewarm":
			if adapter.name != Responses {
				return unsupported("/cache", "target has no cache prewarm mapping")
			}
			if !fields["prewarm"].IsZero() {
				return unsupported("/cache", "one wire field cannot express multiple prewarm intents")
			}
			fields["prewarm"] = intent.Value
		default:
			return unsupported("/cache", "unknown cache intent: "+intent.Kind)
		}
	}
	return nil
}

// encodeParameters 将语义参数映射到目标 wire 字段。Gemini 的参数全部落在
// generationConfig；Anthropic 的 metadata.user_id（Claude Code 恒带）在
// OpenAI 系目标映射为等价 user 字段，完整 metadata 对象仅同族保真。
func (adapter module) encodeParameters(fields p.Object, parameters p.Object) error {
	parameterOutput := fields
	if adapter.name == Gemini {
		parameterOutput = p.Object{}
	}
	for semantic, value := range parameters {
		// Legacy custom definitions and their Agent policies still use this
		// semantic key. Cross-protocol removal requires an explicit policy rule.
		if semantic == "stream_options" && adapter.name == Chat {
			parameterOutput[semantic] = value
			continue
		}
		if strings.HasPrefix(semantic, wireExtensionPrefix) {
			continue
		}
		if semantic == "stream" && adapter.name == Gemini {
			continue // Selected by the endpoint, not a Gemini JSON field.
		}
		isMapped := false
		for wire, name := range parameterFields[adapter.name] {
			if name == semantic {
				parameterOutput[wire] = value
				isMapped = true
				break
			}
		}
		if semantic == "gemini_generation_extensions" && adapter.name == Gemini {
			extra, err := value.ReadObject()
			if err != nil {
				return err
			}
			for key, value := range extra {
				parameterOutput[key] = value
			}
			isMapped = true
		}
		if semantic == "anthropic_metadata" && (adapter.name == Chat || adapter.name == Responses) {
			isMapped = true
		}
		if !isMapped {
			return unsupported("/parameters/"+semantic, "target has no equivalent parameter mapping")
		}
	}
	if (adapter.name == Chat || adapter.name == Responses) && parameterOutput["user"].IsZero() {
		if meta := parameters["anthropic_metadata"]; !meta.IsZero() {
			if object, err := meta.ReadObject(); err == nil {
				if user := object["user_id"]; !user.IsZero() {
					parameterOutput["user"] = user
				}
			}
		}
	}
	if adapter.name == Gemini && len(parameterOutput) > 0 {
		fields["generationConfig"] = object(parameterOutput)
	}
	return nil
}
