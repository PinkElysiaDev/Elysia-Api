package relay

import (
	"context"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// NewProtocolCompiler installs the same four wire adapters used by relay. The
// compiler owns no HTTP clients or credentials; transport is a separate layer.
func NewProtocolCompiler(limits protocol.Limits) (*protocol.Compiler, error) {
	modules := make([]protocol.Module, 0, len(builtinWireAdapters))
	for _, adapter := range builtinWireAdapters {
		modules = append(modules, builtinProtocolModule{adapter: adapter})
	}
	return protocol.NewCompiler(limits, modules, nil)
}

type builtinProtocolModule struct{ adapter builtinWireAdapter }

func (module builtinProtocolModule) Name() string { return module.adapter.name }
func (module builtinProtocolModule) Directions() []protocol.Direction {
	return []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest, protocol.DecodeResponse, protocol.EncodeResponse}
}

func (module builtinProtocolModule) Convert(ctx context.Context, direction protocol.Direction, input protocol.Value, options protocol.EvaluationContext) (protocol.Value, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Value{}, err
	}
	switch direction {
	case protocol.DecodeRequest:
		model := options.Scope.Model
		if pathModel := options.Values["model"]; !pathModel.IsZero() {
			if err := pathModel.Decode(&model); err != nil {
				return protocol.Value{}, err
			}
		}
		request, err := DecodeProtocolSnapshot(input.Bytes(), module.adapter.format, model, module.adapter.Identity(), options.Scope)
		if err != nil {
			return protocol.Value{}, err
		}
		return protocol.EncodeValue(request)
	case protocol.EncodeRequest:
		var request protocol.Request
		if err := input.Decode(&request); err != nil {
			return protocol.Value{}, err
		}
		projected, err := projectProtocolRequest(&request, module.adapter.format)
		if err != nil {
			return protocol.Value{}, err
		}
		body, err := module.adapter.EncodeRequest(projected)
		if err != nil {
			return protocol.Value{}, err
		}
		value, err := protocol.ParseValue(body)
		if err != nil || module.adapter.format != FormatResponses {
			return value, err
		}
		items, err := renderProtocolResponsesInput(request.Content)
		if err != nil {
			return protocol.Value{}, err
		}
		fields, err := value.ReadObject()
		if err != nil {
			return protocol.Value{}, err
		}
		fields["input"], err = protocol.EncodeValue(items)
		if err != nil {
			return protocol.Value{}, err
		}
		delete(fields, "instructions")
		return protocol.EncodeValue(fields)
	case protocol.DecodeResponse:
		response, err := module.adapter.DecodeResponse(input.Bytes())
		if err != nil {
			return protocol.Value{}, err
		}
		canonical, err := snapshotProtocolResponse(response, module.adapter.Identity(), options.Scope)
		if err != nil {
			return protocol.Value{}, err
		}
		return protocol.EncodeValue(canonical)
	case protocol.EncodeResponse:
		var response protocol.Response
		if err := input.Decode(&response); err != nil {
			return protocol.Value{}, err
		}
		projected, err := projectProtocolResponse(&response, module.adapter.format)
		if err != nil {
			return protocol.Value{}, err
		}
		body, err := module.adapter.EncodeResponse(projected)
		if err != nil {
			return protocol.Value{}, err
		}
		return protocol.ParseValue(body)
	default:
		return protocol.Value{}, fmt.Errorf("module %s does not implement %s", module.Name(), direction)
	}
}
