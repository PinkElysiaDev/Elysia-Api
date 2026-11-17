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
		if value := tool.Options["eager_input_streaming"]; ValidateToolStreamOption(value, "") != nil {
			// Formatting is needed only on failure. Re-run the original validator
			// with its owned path so its diagnostic stays byte-identical.
			return ValidateToolStreamOption(value, fmt.Sprintf("/tools/%d/options/eager_input_streaming", i))
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
	return requestCaches(req, func(intents *[]CacheIntent, at diagnosticPath) error {
		for i, intent := range *intents {
			path := at.index(i)
			switch intent.Kind {
			case "key", "retention":
				var value string
				if intent.Value.IsZero() || (!intent.Value.IsNull() && intent.Value.Decode(&value) != nil) {
					return streamIssue(InvalidInput, path.suffix("/value").String(), "cache hint must be a string or null")
				}
				if !intent.TTL.IsZero() || intent.Resource != nil {
					return streamIssue(InvalidInput, path.String(), "cache key and retention cannot carry TTL or a resource")
				}
			case "breakpoint":
				if intent.Resource != nil {
					return streamIssue(InvalidInput, path.String(), "cache breakpoint cannot carry a resource")
				}
				if intent.Value.IsNull() {
					if !intent.TTL.IsZero() {
						return streamIssue(InvalidInput, path.suffix("/ttl").String(), "a null policy cannot contain TTL")
					}
					continue
				}
				fields, err := intent.Value.ReadObject()
				if err != nil {
					return streamIssue(InvalidInput, path.suffix("/value").String(), "cache policy must be an object or null")
				}
				var kind, ttl string
				if fields["type"].IsZero() || fields["type"].IsNull() || fields["type"].Decode(&kind) != nil || kind == "" {
					return streamIssue(InvalidInput, path.suffix("/value/type").String(), "cache policy type must be a nonempty string")
				}
				if !fields["ttl"].IsZero() {
					return streamIssue(InvalidInput, path.suffix("/value/ttl").String(), "move ttl to the cache intent's ttl field")
				}
				if !intent.TTL.IsZero() && !intent.TTL.IsNull() && intent.TTL.Decode(&ttl) != nil {
					return streamIssue(InvalidInput, path.suffix("/ttl").String(), "breakpoint TTL must be a string or null")
				}
			}
		}
		return nil
	})
}

// Visit semantic cache owners only; tool arguments and user JSON are payload.
func requestCaches(req *Request, visit func(*[]CacheIntent, diagnosticPath) error) error {
	var buffer [256]byte
	root := diagnosticPath(buffer[:0])
	if err := visit(&req.Cache, root.suffix("/cache")); err != nil {
		return err
	}
	for index := range req.Tools {
		if err := visit(&req.Tools[index].Cache, root.suffix("/tools").index(index).suffix("/cache")); err != nil {
			return err
		}
	}
	var walk func([]Node, diagnosticPath) error
	walk = func(nodes []Node, at diagnosticPath) error {
		for index := range nodes {
			node := &nodes[index]
			path := at.index(index)
			if err := visit(&node.Cache, path.suffix("/cache")); err != nil {
				return err
			}
			if err := walk(node.Children, path.suffix("/children")); err != nil {
				return err
			}
			if err := walk(node.ReasoningContent, path.suffix("/reasoningContent")); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(req.Content, root.suffix("/content"))
}

func (c *CompiledConversion) requestProjection(ctx context.Context, req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	opts, optionErr := readConversionOption[requestProjectionOptions](c, rule)
	if err := optionErr; err != nil {
		return err
	}
	return c.projectRequestIntents(ctx, req, route, rule, opts.corePlan, sink)
}
