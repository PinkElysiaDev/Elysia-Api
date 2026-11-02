package server

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// writeIngressError maps a host failure through the selected response program.
// An unavailable or failing mapping uses one fixed gateway envelope; it never
// chooses another protocol or retries encoding through a different engine.
func (s *Server) writeIngressError(ctx context.Context, c *app.RequestContext, failure *builtin.GatewayError) error {
	selection := s.pinIngress(c)
	status := failure.EffectiveStatus()
	err := selection.err
	if err == nil {
		payload, encodeErr := protocol.EncodeValue(struct {
			Category string `json:"category"`
			Message  string `json:"message"`
			Code     string `json:"code,omitempty"`
			Param    string `json:"param,omitempty"`
		}{string(failure.Class.OrDefault()), failure.Message, failure.Code, failure.Param})
		if encodeErr != nil {
			panic(encodeErr) // This fixed string-only object is always encodable.
		}
		statusValue, encodeErr := protocol.EncodeValue(status)
		if encodeErr != nil {
			panic(encodeErr)
		}
		options := protocol.EvaluationContext{Values: protocol.Object{"httpStatus": statusValue}}
		response := &protocol.Response{SchemaVersion: selection.compiled.CoreVersion(), Error: payload}
		var wire protocol.Value
		wire, err = encodeFailureResponse(ctx, selection.compiled, response, options)
		if err == nil {
			c.Data(status, contentTypeJSON, wire.Bytes())
			return nil
		}
	}
	s.logEvent("warn").Err(err).Int("status", status).Msg("ingress error mapping unavailable")
	writeGatewayError(c, status, failure.Message)
	return err
}

func encodeFailureResponse(ctx context.Context, compiled *protocol.Compiled, response *protocol.Response, options protocol.EvaluationContext) (protocol.Value, error) {
	wire, err := compiled.EncodeResponseValue(ctx, response, options)
	if err != nil {
		return protocol.Value{}, err
	}
	return wire, compiled.ValidateResponseOutput(ctx, wire, wire, options)
}

func writeGatewayError(c *app.RequestContext, status int, message string) {
	c.JSON(status, struct {
		Error adminError `json:"error"`
	}{Error: adminError{Code: "protocol_gateway_error", Message: message}})
}
