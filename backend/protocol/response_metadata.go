package protocol

import (
	"fmt"
	"strings"
)

// ResponseMetadata is a recognized wire field, attached to its owning response
// or node. Path and SourceCodec are diagnostic provenance, not target routing.
// Unknown fields remain in Native/Unmapped and retain their existing guards.
type ResponseMetadata struct {
	Name        string `json:"name"`
	Location    string `json:"location"`
	Codec       string `json:"codec"`
	SourceCodec string `json:"sourceCodec"`
	Path        string `json:"path"`
	Value       Value  `json:"value"`
}

// MetadataFieldType is shared by JSON and SSE decoders. Stateful references,
// credentials, signatures and counters are deliberately absent from this list.
func MetadataFieldType(codec, location, name string) string {
	if codec == "responses" && name == "obfuscation" {
		switch location {
		case "event:response.output_text.delta", "event:response.refusal.delta", "event:response.function_call_arguments.delta", "event:response.custom_tool_call_input.delta", "event:response.reasoning_text.delta", "event:response.reasoning_summary_text.delta":
			return "padding-string"
		}
	}
	if location == "response" {
		switch codec {
		case "openai-chat":
			switch name {
			case "service_tier", "system_fingerprint":
				return "string"
			}
		case "responses":
			switch name {
			case "service_tier", "user", "safety_identifier", "prompt_cache_key", "prompt_cache_retention", "truncation":
				return "string"
			case "store", "background", "parallel_tool_calls":
				return "bool"
			case "temperature", "top_p", "max_output_tokens", "max_tool_calls", "top_logprobs", "completed_at", "frequency_penalty", "presence_penalty":
				return "number"
			case "previous_response_id":
				return "reference"
			case "moderation":
				return "empty-state"
			case "access_programs":
				return "access-programs"
			case "tool_usage":
				return "empty-tool-usage"
			case "content_filters":
				// Only the observed empty sentinel has a known meaning. A
				// non-null vendor filtering payload remains an opaque extension.
				return "null-only"
			case "metadata", "reasoning", "text":
				return "object"
			case "tools":
				return "array"
			case "instructions", "tool_choice":
				return "string-or-container"
			}
		case "anthropic":
			if name == "container" || name == "context_management" || name == "stop_details" {
				return "empty-state"
			}
		case "gemini":
			if name == "createTime" {
				return "string"
			}
		}
	}
	if location == "choice" && codec == "openai-chat" && name == "logprobs" {
		return "object"
	}
	// OpenRouter-compatible channels expose the original provider stop spelling
	// alongside finish_reason. It supplements, never replaces, the mapped reason.
	if location == "choice" && codec == "openai-chat" && name == "native_finish_reason" {
		return "string"
	}
	if location == "message" && codec == "openai-chat" && name == "annotations" {
		return "array"
	}
	if location == "message" && codec == "responses" && name == "phase" {
		return "message-phase"
	}
	if location == "message" && codec == "responses" && (name == "metadata" || name == "internal_chat_message_metadata_passthrough") {
		return "turn-tags"
	}
	if location == "content" {
		if codec == "responses" && (name == "annotations" || name == "logprobs") {
			return "array"
		}
		if codec == "anthropic" && name == "citations" {
			return "array"
		}
	}
	if location == "candidate" && codec == "gemini" {
		switch name {
		case "finishMessage":
			return "string"
		case "avgLogprobs":
			return "number"
		case "logprobsResult", "citationMetadata", "groundingMetadata":
			return "object"
		case "safetyRatings":
			return "array"
		}
	}
	if location == "usage" && codec == "anthropic" {
		switch name {
		case "service_tier", "inference_geo":
			return "string"
		case "server_tool_use":
			return "object"
		}
	}
	return ""
}

func ValidateMetadataValue(codec, location, name string, v Value, at string) error {
	kind := MetadataFieldType(codec, location, name)
	if kind == "" {
		return streamIssue(UnsupportedCapability, at, "unknown response metadata")
	}
	if v.IsNull() && kind != "padding-string" {
		return nil
	}
	ok := false
	switch kind {
	case "turn-tags":
		fields, err := v.ReadObject()
		if err != nil {
			break
		}
		for _, key := range sortedKeys(fields) {
			if key != "turn_id" {
				return streamIssue(UnsupportedCapability, at+"/"+key, "unknown message metadata requires explicit mapping")
			}
			var id string
			if fields[key].IsNull() || fields[key].Decode(&id) != nil || strings.TrimSpace(id) == "" {
				return streamIssue(InvalidInput, at+"/"+key, "message turn tag must be a nonempty string")
			}
		}
		ok = true
	case "padding-string":
		var value string
		ok = !v.IsNull() && v.Decode(&value) == nil
	case "empty-tool-usage":
		ok = EmptyResponsesToolUsage(v)
	case "access-programs":
		fields, err := v.ReadObject()
		if err != nil {
			break
		}
		for _, key := range sortedKeys(fields) {
			if key != "cyber" {
				return streamIssue(UnsupportedCapability, at+"/"+key, "unknown response access program requires explicit mapping")
			}
		}
		switch fields["cyber"] {
		case StringValue("standard"), StringValue("daybreak_blue"), StringValue("daybreak_red"):
			ok = true
		default:
			return streamIssue(InvalidInput, at+"/cyber", "expected standard, daybreak_blue or daybreak_red access program")
		}
	case "message-phase":
		ok = v == StringValue("commentary") || v == StringValue("final_answer")
	case "string", "reference":
		var s string
		ok = v.Decode(&s) == nil
		if kind == "reference" {
			ok = ok && strings.TrimSpace(s) != ""
		}
	case "bool":
		var b bool
		ok = v.Decode(&b) == nil
	case "number":
		raw := strings.TrimSpace(string(v.Bytes()))
		ok = len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9')
	case "object", "empty-state":
		ok = v.IsObject()
	case "array":
		var a []Value
		ok = v.Decode(&a) == nil
	case "string-or-container":
		var s string
		var a []Value
		ok = v.IsObject() || v.Decode(&s) == nil || v.Decode(&a) == nil
	}
	if !ok {
		return streamIssue(InvalidInput, at, "invalid response metadata type; expected "+kind+" or null")
	}
	if (name == "annotations" || name == "citations") && kind == "array" {
		var entries []Value
		_ = v.Decode(&entries)
		for i, entry := range entries {
			if !entry.IsObject() {
				return streamIssue(InvalidInput, fmt.Sprintf("%s/%d", at, i), "citation must be an object")
			}
		}
	}
	return nil
}

func emptyMetadata(v Value) bool {
	s := strings.TrimSpace(string(v.Bytes()))
	return s == "null" || s == "[]" || s == "{}"
}

// Only the observed correlation tag is classified. Other channel annotations
// retain their opaque boundary, including when they accompany a known tag.
func KnownResponseTurnTags(v Value) bool {
	fields, err := v.ReadObject()
	if err != nil {
		return true
	} // Type/null validation remains the caller's job.
	for key := range fields {
		if key != "turn_id" {
			return false
		}
	}
	return true
}

func emptyMetadataField(item ResponseMetadata) bool {
	if item.Codec == "responses" && item.Name == "tool_usage" {
		return EmptyResponsesToolUsage(item.Value)
	}
	if emptyMetadata(item.Value) {
		return true
	}
	// Anthropic explicitly reports no applied context edits this way. Other
	// keys or any actual edit still require state-aware conversion.
	if item.Codec == "anthropic" && item.Name == "context_management" {
		fields, err := item.Value.ReadObject()
		return err == nil && len(fields) == 1 && string(fields["applied_edits"].Bytes()) == "[]"
	}
	return false
}

func (c *CompiledConversion) projectMetadata(items []ResponseMetadata, codec string, phase ConversionPhase, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) ([]ResponseMetadata, error) {
	var out []ResponseMetadata
	for _, item := range items {
		if err := ValidateMetadataValue(item.Codec, item.Location, item.Name, item.Value, item.Path); err != nil {
			return nil, err
		}
		if item.Codec == codec {
			out = append(out, item)
			continue
		}
		if MetadataFieldType(item.Codec, item.Location, item.Name) == "padding-string" {
			if err := c.issue(rule, phase, route, item.Path, "source event padding cannot preserve payload-size obfuscation after protocol conversion", sink, true); err != nil {
				return nil, err
			}
			continue
		}
		if (item.Name == "annotations" || item.Name == "citations" || item.Name == "logprobs") && emptyMetadata(item.Value) {
			sink.Add(ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved", Protocol: route.Target, Stage: "conversion." + string(phase), Path: item.Path, RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Reason: "empty known response metadata normalized for target", Evidence: c.Origins[rule.ID]})
			continue
		}
		if item.Name == "annotations" && !emptyMetadata(item.Value) {
			if _, _, err := mapPublicMetadata(item, map[string]string{"openai-chat": "responses", "responses": "openai-chat"}[item.Codec]); err != nil {
				return nil, err
			}
		}
		kind := MetadataFieldType(item.Codec, item.Location, item.Name)
		if (kind == "empty-state" || kind == "reference") && !emptyMetadataField(item) {
			return nil, streamIssue(UnsupportedCapability, item.Path, "nonempty context/container state requires a scoped state mapping")
		}
		if (item.Name == "citations" || item.Name == "citationMetadata" || item.Name == "groundingMetadata") && !emptyMetadata(item.Value) {
			return nil, streamIssue(UnsupportedCapability, item.Path, "non-URL citation or grounding resources require an explicit scoped mapping")
		}
		mapped, ok, err := mapPublicMetadata(item, codec)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, mapped)
			continue
		}
		if emptyMetadataField(item) {
			sink.Add(ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved", Protocol: route.Target, Stage: "conversion." + string(phase), Path: item.Path, RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Reason: "empty known response metadata normalized for target", Evidence: c.Origins[rule.ID]})
		} else if err := c.issue(rule, phase, route, item.Path, "target cannot express this recognized response metadata field", sink, true); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func mapPublicMetadata(item ResponseMetadata, codec string) (ResponseMetadata, bool, error) {
	openAI := func(s string) bool { return s == "openai-chat" || s == "responses" }
	if !openAI(item.Codec) || !openAI(codec) {
		return item, false, nil
	}
	if item.Name == "service_tier" {
		item.Codec = codec
		return item, true, nil
	}
	if item.Name == "annotations" {
		var entries []Value
		if item.Value.IsNull() {
			return item, false, nil
		}
		if err := item.Value.Decode(&entries); err != nil {
			return item, false, err
		}
		for i, entry := range entries {
			fields, err := entry.ReadObject()
			if err != nil {
				return item, false, err
			}
			if fields["type"] != StringValue("url_citation") {
				return item, false, streamIssue(UnsupportedCapability, fmt.Sprintf("%s/%d", item.Path, i), "non-URL citation requires a resource mapping")
			}
			if item.Codec == "openai-chat" {
				for k := range fields {
					if k != "type" && k != "url_citation" {
						return item, false, streamIssue(UnsupportedCapability, item.Path+"/"+k, "unknown citation extension requires explicit mapping")
					}
				}
				fields, err = fields["url_citation"].ReadObject()
				if err != nil {
					return item, false, err
				}
				fields["type"] = StringValue("url_citation")
			}
			for k := range fields {
				if k != "type" && k != "url" && k != "title" && k != "start_index" && k != "end_index" {
					return item, false, streamIssue(UnsupportedCapability, item.Path+"/"+k, "unknown citation extension requires explicit mapping")
				}
			}
			for _, key := range []string{"url", "title"} {
				var s string
				if fields[key].Decode(&s) != nil {
					return item, false, streamIssue(InvalidInput, item.Path, "URL citation requires "+key)
				}
			}
			for _, key := range []string{"start_index", "end_index"} {
				var n int64
				if fields[key].Decode(&n) != nil || n < 0 {
					return item, false, streamIssue(InvalidInput, item.Path, "URL citation requires nonnegative text offsets")
				}
			}
			var start, end int64
			_ = fields["start_index"].Decode(&start)
			_ = fields["end_index"].Decode(&end)
			if end < start {
				return item, false, streamIssue(InvalidInput, item.Path, "citation end precedes its start")
			}
			if codec == "openai-chat" {
				delete(fields, "type")
				v, _ := EncodeValue(fields)
				fields = Object{"type": StringValue("url_citation"), "url_citation": v}
			}
			entries[i], _ = EncodeValue(fields)
		}
		item.Value, _ = EncodeValue(entries)
		item.Codec = codec
		item.Location = "content"
		if codec == "openai-chat" {
			item.Location = "message"
		}
		return item, true, nil
	}
	if item.Name == "logprobs" {
		if item.Value.IsNull() {
			return item, false, nil
		}
		if codec == "responses" {
			fields, err := item.Value.ReadObject()
			if err != nil {
				return item, false, err
			}
			for k, v := range fields {
				if k != "content" && k != "refusal" {
					return item, false, streamIssue(UnsupportedCapability, item.Path+"/"+k, "unknown logprobs field requires explicit mapping")
				}
				if k != "content" && !emptyMetadata(v) {
					return item, false, nil
				}
			}
			if fields["content"].IsZero() || fields["content"].IsNull() {
				return item, false, nil
			}
			item.Value = fields["content"]
			if err := validateLogprobTokens(item.Value, item.Path+"/content"); err != nil {
				return item, false, err
			}
			item.Location = "content"
		} else {
			if err := validateLogprobTokens(item.Value, item.Path); err != nil {
				return item, false, err
			}
			item.Value, _ = EncodeValue(Object{"content": item.Value})
			item.Location = "choice"
		}
		item.Codec = codec
		return item, true, nil
	}
	return item, false, nil
}

func validateLogprobTokens(value Value, at string) error {
	var entries []Value
	if value.IsNull() || value.Decode(&entries) != nil {
		return streamIssue(InvalidInput, at, "logprobs must be an array")
	}
	var token func(Value, string, bool) error
	token = func(value Value, at string, top bool) error {
		fields, err := value.ReadObject()
		if err != nil {
			return streamIssue(InvalidInput, at, "logprob token must be an object")
		}
		for k := range fields {
			if k != "token" && k != "logprob" && k != "bytes" && !(k == "top_logprobs" && !top) {
				return streamIssue(UnsupportedCapability, at+"/"+k, "unknown token probability field")
			}
		}
		var text string
		var prob float64
		if fields["token"].IsNull() || fields["token"].Decode(&text) != nil {
			return streamIssue(InvalidInput, at+"/token", "token must be a string")
		}
		if fields["logprob"].IsNull() || fields["logprob"].Decode(&prob) != nil {
			return streamIssue(InvalidInput, at+"/logprob", "logprob must be a number")
		}
		if b := fields["bytes"]; !b.IsZero() && !b.IsNull() {
			var bytes []int
			if b.Decode(&bytes) != nil {
				return streamIssue(InvalidInput, at+"/bytes", "token bytes must be an integer array")
			}
			for _, n := range bytes {
				if n < 0 || n > 255 {
					return streamIssue(InvalidInput, at+"/bytes", "byte outside 0..255")
				}
			}
		}
		if alternatives := fields["top_logprobs"]; !alternatives.IsZero() && !alternatives.IsNull() {
			var a []Value
			if alternatives.Decode(&a) != nil {
				return streamIssue(InvalidInput, at+"/top_logprobs", "expected array")
			}
			for i, v := range a {
				if err := token(v, fmt.Sprintf("%s/top_logprobs/%d", at, i), true); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, v := range entries {
		if err := token(v, fmt.Sprintf("%s/%d", at, i), false); err != nil {
			return err
		}
	}
	return nil
}

func (c *CompiledConversion) metadataValue(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	var codec string
	_ = rule.Value.Decode(&codec)
	var visit func(*Node) error
	visit = func(n *Node) error {
		var err error
		n.Metadata, err = c.projectMetadata(n.Metadata, codec, phase, route, rule, sink)
		if err != nil {
			return err
		}
		for i := range n.Children {
			if err = visit(&n.Children[i]); err != nil {
				return err
			}
		}
		for i := range n.ReasoningContent {
			if err = visit(&n.ReasoningContent[i]); err != nil {
				return err
			}
		}
		if codec == "responses" && n.Kind == MessageNode {
			var keep []ResponseMetadata
			for _, m := range n.Metadata {
				if m.Location != "content" {
					keep = append(keep, m)
					continue
				}
				textIndex := -1
				for i := range n.Children {
					if n.Children[i].Kind == TextNode {
						if textIndex != -1 {
							return streamIssue(UnsupportedCapability, m.Path, "message metadata requires unambiguous text ownership")
						}
						textIndex = i
					}
				}
				if textIndex == -1 {
					return streamIssue(UnsupportedCapability, m.Path, "content metadata has no text owner")
				}
				n.Children[textIndex].Metadata = append(n.Children[textIndex].Metadata, m)
			}
			n.Metadata = keep
		}
		return nil
	}
	response := func(r *Response) error {
		if r == nil {
			return nil
		}
		var err error
		r.Metadata, err = c.projectMetadata(r.Metadata, codec, phase, route, rule, sink)
		if err != nil {
			return err
		}
		for i := range r.Content {
			if err = visit(&r.Content[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if phase == ConversionRequest {
		var r Request
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		for i := range r.Content {
			if err := visit(&r.Content[i]); err != nil {
				return Value{}, err
			}
		}
		return EncodeValue(r)
	}
	if phase == ConversionResponse {
		var r Response
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		if err := response(&r); err != nil {
			return Value{}, err
		}
		return EncodeValue(r)
	}
	var e Event
	if err := input.Decode(&e); err != nil {
		return Value{}, err
	}
	var err error
	e.Metadata, err = c.projectMetadata(e.Metadata, codec, phase, route, rule, sink)
	if err != nil {
		return Value{}, err
	}
	if err = response(e.Response); err != nil {
		return Value{}, err
	}
	if e.Item != nil {
		if err = visit(e.Item); err != nil {
			return Value{}, err
		}
	}
	return EncodeValue(e)
}
