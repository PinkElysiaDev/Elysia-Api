package protocol

import "fmt"

type requestProjectionOptions struct {
	SourceCodec string `json:"sourceCodec,omitempty"`
	TargetCodec string `json:"targetCodec"`
}

// Hint validation runs before authored rules and projection can remove fields.
func validateRequestHints(req *Request) error {
	for _, key := range []string{"metadata", "anthropic_metadata", "user"} {
		value := req.Parameters[key]
		if value.IsZero() || value.IsNull() {
			continue
		}
		at := "/parameters/" + key
		if key == "user" {
			var user string
			if value.Decode(&user) != nil {
				return streamIssue(InvalidInput, at, "user must be a string or null")
			}
			continue
		}
		fields, err := value.ReadObject()
		if err != nil {
			return streamIssue(InvalidInput, at, "tracking metadata must be an object or null")
		}
		for _, name := range sortedKeys(fields) {
			if key == "anthropic_metadata" && name != "user_id" {
				continue // Unknown native fields require explicit cross-protocol mapping.
			}
			var text string
			if fields[name].IsNull() && key == "anthropic_metadata" {
				continue
			}
			if fields[name].IsNull() || fields[name].Decode(&text) != nil {
				return streamIssue(InvalidInput, at+"/"+escapePointer(name), "tracking metadata values must be strings")
			}
		}
	}
	for i, tool := range req.Tools {
		if err := ValidateToolStreamOption(tool.Options["eager_input_streaming"], fmt.Sprintf("/tools/%d/options/eager_input_streaming", i)); err != nil {
			return err
		}
		if value := tool.Options["strict"]; !value.IsZero() && !value.IsNull() {
			var strict bool
			if value.Decode(&strict) != nil {
				return streamIssue(InvalidInput, fmt.Sprintf("/tools/%d/options/strict", i), "tool strict must be a boolean or null")
			}
		}
	}
	return requestCaches(req, func(intents *[]CacheIntent, at string) error {
		for i, intent := range *intents {
			path := fmt.Sprintf("%s/%d", at, i)
			switch intent.Kind {
			case "key", "retention":
				var value string
				if intent.Value.IsZero() || (!intent.Value.IsNull() && intent.Value.Decode(&value) != nil) {
					return streamIssue(InvalidInput, path+"/value", "cache hint must be a string or null")
				}
				if !intent.TTL.IsZero() || intent.Resource != nil {
					return streamIssue(InvalidInput, path, "cache key and retention cannot carry TTL or a resource")
				}
			case "breakpoint":
				if intent.Resource != nil {
					return streamIssue(InvalidInput, path, "cache breakpoint cannot carry a resource")
				}
				if intent.Value.IsNull() {
					if !intent.TTL.IsZero() {
						return streamIssue(InvalidInput, path+"/ttl", "a null policy cannot contain TTL")
					}
					continue
				}
				fields, err := intent.Value.ReadObject()
				if err != nil {
					return streamIssue(InvalidInput, path+"/value", "cache policy must be an object or null")
				}
				var kind, ttl string
				if fields["type"].IsZero() || fields["type"].IsNull() || fields["type"].Decode(&kind) != nil || kind == "" {
					return streamIssue(InvalidInput, path+"/value/type", "cache policy type must be a nonempty string")
				}
				if !fields["ttl"].IsZero() {
					return streamIssue(InvalidInput, path+"/value/ttl", "move ttl to the cache intent's ttl field")
				}
				if !intent.TTL.IsZero() && !intent.TTL.IsNull() && intent.TTL.Decode(&ttl) != nil {
					return streamIssue(InvalidInput, path+"/ttl", "breakpoint TTL must be a string or null")
				}
			}
		}
		return nil
	})
}

// Visit semantic cache owners only; tool arguments and user JSON are payload.
func requestCaches(req *Request, visit func(*[]CacheIntent, string) error) error {
	if err := visit(&req.Cache, "/cache"); err != nil {
		return err
	}
	for i := range req.Tools {
		if err := visit(&req.Tools[i].Cache, fmt.Sprintf("/tools/%d/cache", i)); err != nil {
			return err
		}
	}
	var walk func([]Node, string) error
	walk = func(nodes []Node, at string) error {
		for i := range nodes {
			n := &nodes[i]
			path := fmt.Sprintf("%s/%d", at, i)
			if err := visit(&n.Cache, path+"/cache"); err != nil {
				return err
			}
			if err := walk(n.Children, path+"/children"); err != nil {
				return err
			}
			if err := walk(n.ReasoningContent, path+"/reasoningContent"); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(req.Content, "/content")
}

func (c *CompiledConversion) requestProjection(req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var opts requestProjectionOptions
	if err := rule.Value.Decode(&opts); err != nil {
		return err
	}
	codec := opts.TargetCodec
	openAI := codec == "openai-chat" || codec == "responses"
	omit := func(at string, empty bool, severity Severity) error {
		if empty {
			c.normalized(rule, route, at, "empty request hint normalized for target", sink)
			return nil
		}
		return c.issueSeverity(rule, ConversionRequest, route, at, "request hint omitted: target has no equivalent mapping", sink, true, severity)
	}
	if err := requestCaches(req, func(intents *[]CacheIntent, at string) error {
		var kept []CacheIntent
		for i, intent := range *intents {
			known, supported := false, false
			switch intent.Kind {
			case "key":
				known, supported = intent.Location == "request", openAI
			case "retention":
				known = intent.Location == "request" && (intent.Value.IsNull() || intent.Value.IsString("in_memory") || intent.Value.IsString("24h"))
				supported = openAI
			case "breakpoint":
				fields, _ := intent.Value.ReadObject()
				known = intent.Value.IsNull() || (len(fields) == 1 && fields["type"].IsString("ephemeral") && (intent.TTL.IsZero() || intent.TTL.IsNull() || intent.TTL.IsString("5m") || intent.TTL.IsString("1h")))
				supported = codec == "anthropic"
			}
			if !known && (intent.Kind == "breakpoint" || intent.Kind == "key" || intent.Kind == "retention") && (route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion) {
				return candidateFailure(streamIssue(UnsupportedCapability, fmt.Sprintf("%s/%d", at, i), "unknown cache policy requires explicit mapping"))
			}
			if !known || supported {
				kept = append(kept, intent)
				continue
			}
			if err := omit(fmt.Sprintf("%s/%d", at, i), intent.Value.IsNull(), SeverityInfo); err != nil {
				return err
			}
		}
		*intents = kept
		return nil
	}); err != nil {
		return err
	}
	for _, key := range []string{"metadata", "user", "anthropic_metadata"} {
		value := req.Parameters[key]
		if value.IsZero() || (key != "anthropic_metadata" && openAI) || (key == "anthropic_metadata" && codec == "anthropic") {
			continue
		}
		at := "/parameters/" + key
		fields, _ := value.ReadObject()
		if key == "anthropic_metadata" {
			for _, field := range sortedKeys(fields) {
				if field != "user_id" {
					return candidateFailure(streamIssue(UnsupportedCapability, at+"/"+escapePointer(field), "unknown tracking metadata requires explicit mapping"))
				}
			}
			if openAI {
				if user := fields["user_id"]; !user.IsZero() {
					if existing := req.Parameters["user"]; !existing.IsZero() && !equalValues(existing, user) {
						return candidateFailure(streamIssue(InvalidInput, at+"/user_id", "conflicting user identities"))
					}
					req.Parameters["user"] = user
				}
				delete(req.Parameters, key)
				c.normalized(rule, route, at, "tracking user identity mapped to target user field", sink)
				continue
			}
		}
		if err := omit(at, value.IsNull() || (value.IsObject() && len(fields) == 0), SeverityInfo); err != nil {
			return err
		}
		delete(req.Parameters, key)
	}
	for i := range req.Tools {
		tool := &req.Tools[i]
		if tool.Kind != FunctionTool {
			continue
		}
		strict := tool.Options["strict"]
		absent := strict.IsZero() || strict.IsNull()
		at := fmt.Sprintf("/tools/%d/options/strict", i)
		if opts.SourceCodec != codec && absent {
			delete(tool.Options, "strict")
			if opts.SourceCodec == "responses" {
				if err := c.issue(rule, ConversionRequest, route, at, "Responses automatic tool strictness has no equivalent target default", sink, true); err != nil {
					return err
				}
			} else if codec == "responses" && (opts.SourceCodec == "openai-chat" || opts.SourceCodec == "anthropic") {
				if tool.Options == nil {
					tool.Options = Object{}
				}
				tool.Options["strict"] = controlValue(false)
				c.normalized(rule, route, at, "non-strict source tool default made explicit for Responses", sink)
			}
			continue
		}
		if !openAI && codec != "anthropic" && !strict.IsZero() {
			if err := omit(at, strict.IsNull(), SeverityWarning); err != nil {
				return err
			}
			delete(tool.Options, "strict")
		}
	}
	return nil
}
