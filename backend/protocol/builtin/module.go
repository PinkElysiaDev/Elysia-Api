// Package builtin implements vendor wire formats directly against the ordered
// protocol contract. It contains no gateway routing or legacy message model.
package builtin

import (
	"context"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

const (
	Chat        = "openai-chat"
	Responses   = "responses"
	Anthropic   = "anthropic"
	Gemini      = "gemini"
	WireVersion = "v2"
)

type module struct{ name, family string }

var modules = []module{{Chat, "openai_chat"}, {Responses, "openai_responses"}, {Anthropic, "claude"}, {Gemini, "gemini"}}

// Modules returns stateless adapters suitable for an immutable compiler registry.
func Modules() []p.Module {
	result := make([]p.Module, len(modules))
	for index, entry := range modules {
		result[index] = entry
	}
	return result
}

func (adapter module) Name() string { return adapter.name }
func (adapter module) Directions() []p.Direction {
	return []p.Direction{p.DecodeRequest, p.EncodeRequest, p.DecodeResponse, p.EncodeResponse, p.DecodeEvent, p.EncodeEvent}
}
func (adapter module) Convert(ctx context.Context, direction p.Direction, input p.Value, options p.EvaluationContext) (p.Value, error) {
	if err := ctx.Err(); err != nil {
		return p.Value{}, err
	}
	switch direction {
	case p.DecodeRequest:
		request, err := adapter.decodeRequest(input, options)
		if err != nil {
			return p.Value{}, err
		}
		return p.EncodeValue(request)
	case p.EncodeRequest:
		var request p.Request
		if err := input.Decode(&request); err != nil {
			return p.Value{}, err
		}
		return adapter.encodeRequest(&request, options)
	case p.DecodeResponse:
		response, err := adapter.decodeResponse(input, options)
		if err != nil {
			return p.Value{}, err
		}
		return p.EncodeValue(response)
	case p.EncodeResponse:
		var response p.Response
		if err := input.Decode(&response); err != nil {
			return p.Value{}, err
		}
		return adapter.encodeResponse(&response, options)
	default:
		return p.Value{}, fmt.Errorf("%s does not support %s", adapter.name, direction)
	}
}

func (adapter module) native(value p.Value, path string, direction p.Direction, options p.EvaluationContext) *p.Native {
	return &p.Native{Source: p.Provenance{Protocol: options.Identity(), Direction: direction, Path: path, Scope: options.Scope}, Value: value}
}

func unsupported(path, reason string) error {
	return p.IssuesError([]p.ConversionIssue{{Code: p.UnsupportedCapability, Severity: p.SeverityError, Stage: "wire", Path: path, Reason: reason, Suggestion: "Use a target with an equivalent declared mapping; preserve the original capability."}})
}

func (adapter module) replay(native *p.Native, direction p.Direction, options p.EvaluationContext) (p.Value, error) {
	if native == nil {
		return p.Value{}, unsupported("/native", "native node has no equivalent in the target protocol")
	}
	target := p.Target{Protocol: options.Identity(), Direction: direction, Scope: options.Scope}
	value, issues := p.PreserveNative(*native, target, nil, p.DefaultLimits())
	return value, p.IssuesError(issues)
}
