package builtin

import (
	"context"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) DecodeRequest(ctx context.Context, input p.Value, options p.EvaluationContext) (*p.Request, error) {
	return adapter.decodeRequest(input, options)
}
func (adapter module) EncodeRequest(ctx context.Context, input *p.Request, options p.EvaluationContext) (p.Value, error) {
	return adapter.encodeRequest(input, options)
}
func (adapter module) DecodeResponse(ctx context.Context, input p.Value, options p.EvaluationContext) (*p.Response, error) {
	return adapter.decodeResponse(input, options)
}
func (adapter module) EncodeResponse(ctx context.Context, input *p.Response, options p.EvaluationContext) (p.Value, error) {
	return adapter.encodeResponse(input, options)
}
