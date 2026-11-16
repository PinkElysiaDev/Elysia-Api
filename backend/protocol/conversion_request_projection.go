package protocol

import (
	"context"
	"fmt"
)

type requestProjectionOptions struct {
	Core     *requestProjectionSpec `json:"core,omitempty"`
	corePlan *compiledRequestProjection
}

// Hint validation runs before authored rules and projection can remove fields.
func validateRequestHints(req *Request) error {
	return validateRequestIntentShapes(req)
}

func validateRequestIntentShapes(req *Request) error {
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
		if value := tool.Options["strictDefault"]; !value.IsZero() && !value.IsString("automatic") && !value.IsString("disabled") {
			return streamIssue(InvalidInput, fmt.Sprintf("/tools/%d/options/strictDefault", i), "tool strictness default must be automatic or disabled")
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

func (c *CompiledConversion) requestProjection(ctx context.Context, req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	opts, optionErr := readConversionOption[requestProjectionOptions](c, rule)
	if err := optionErr; err != nil {
		return err
	}
	return c.projectRequestIntents(ctx, req, route, rule, opts.corePlan, sink)
}
