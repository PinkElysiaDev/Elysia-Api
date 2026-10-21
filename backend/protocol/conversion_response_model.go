package protocol

import (
	"context"
	"errors"
	"strings"
)

// ResponseModelOrigin describes one observed response, independently of later
// semantic mappings. RequestModel comes from the final outbound wire request.
type ResponseModelOrigin struct {
	RequestModel  Value `json:"requestModel,omitzero"`
	SourcePresent bool  `json:"sourcePresent"`
	SourceModel   Value `json:"sourceModel,omitzero"`
}

// AttributeResponseModelFailure attributes a failed model contract to the
// provider only when both semantic and final wire values still match the
// observed source. Mapping-created errors retain their target diagnostics.
func (route ConversionContext) AttributeResponseModelFailure(err error, response *Response, wire Value) error {
	origin := route.ResponseModel
	var conversion *ConversionError
	if origin == nil || response == nil || !errors.As(err, &conversion) || origin.SourceModel != response.Model {
		return err
	}
	fields, readErr := wire.ReadObject()
	model, isPresent := fields["model"]
	if readErr != nil || isPresent != origin.SourcePresent || model != origin.SourceModel {
		return err
	}
	issues := append([]ConversionIssue(nil), conversion.Issues...)
	for index := range issues {
		issue := &issues[index]
		if issue.Stage == "wire.output" && issue.Path == "/model" {
			issue.Code, issue.Protocol, issue.Direction = UpstreamContractViolation, route.Source, DecodeResponse
			issue.Stage = "decode.response"
			issue.Reason = "provider response model must be a nonempty string"
			issue.Suggestion = "Correct the provider response; authored mappings and final output checks remain enforced."
		}
	}
	return &ConversionError{Issues: issues}
}

func chatResponseModelPair(ingress, upstream *Compiled) bool {
	return ingress.Codec(DecodeRequest) == "openai-chat" && ingress.Codec(EncodeResponse) == "openai-chat" &&
		upstream.Codec(EncodeRequest) == "openai-chat" && upstream.Codec(DecodeResponse) == "openai-chat"
}

// WithResponseModelOrigin captures only model fields; it never copies a prompt
// into conversion context or substitutes the routing model for the sent model.
func (route ConversionContext) WithResponseModelOrigin(ingress, upstream *Compiled, outbound, source []byte) (ConversionContext, error) {
	route.ResponseModel = nil
	if route.Transport != HTTPJSON || !chatResponseModelPair(ingress, upstream) {
		return route, nil
	}
	request, err := ParseValue(outbound)
	if err != nil {
		return route, err
	}
	route, err = route.WithRequestModelOrigin(ingress, upstream, request)
	if err != nil {
		return route, err
	}
	return route.WithObservedResponseModel(source)
}

// WithRequestModelOrigin snapshots only the final outbound model at dispatch.
// No request body has to survive provider I/O for later response attribution.
func (route ConversionContext) WithRequestModelOrigin(ingress, upstream *Compiled, outbound Value) (ConversionContext, error) {
	route.ResponseModel = nil
	if route.Transport != HTTPJSON || !chatResponseModelPair(ingress, upstream) {
		return route, nil
	}
	fields, err := (EvaluationContext{}).WithValueReader(outbound).ReadObject(outbound)
	if err != nil {
		return route, err
	}
	model := fields["model"]
	route.ResponseModel = &ResponseModelOrigin{RequestModel: Value{raw: strings.Clone(model.raw)}}
	return route, nil
}

// WithObservedResponseModel completes a dispatch-time snapshot without mutating
// a reusable route. Absence remains distinct from null or an invalid model.
func (route ConversionContext) WithObservedResponseModel(source []byte) (ConversionContext, error) {
	if route.ResponseModel == nil {
		return route, nil
	}
	value, err := ParseValue(source)
	if err != nil {
		return route, err
	}
	response, err := (EvaluationContext{}).WithValueReader(value).ReadObject(value)
	if err != nil {
		return route, err
	}
	model, isPresent := response["model"]
	origin := *route.ResponseModel
	origin.SourcePresent, origin.SourceModel = isPresent, Value{raw: strings.Clone(model.raw)}
	route.ResponseModel = &origin
	return route, nil
}

func (c *CompiledConversion) repairChatResponseModel(response *Response, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	if route.Transport != HTTPJSON || !response.Model.IsZero() || !response.Status.IsString("completed") || (!response.Error.IsZero() && !response.Error.IsNull()) {
		return nil
	}
	origin := route.ResponseModel
	if origin == nil {
		return streamIssue(InvalidInput, "/context/responseModel", "model repair requires the observed source field and final outbound request model")
	}
	if origin.SourcePresent || !origin.SourceModel.IsZero() {
		// A mapping removed a field that existed upstream. Repairing it here
		// would conceal the authored mapping's effect.
		return nil
	}
	var model string
	if origin.RequestModel.Decode(&model) != nil || model == "" {
		return streamIssue(InvalidInput, "/context/responseModel/requestModel", "model repair requires a nonempty model from the final outbound request")
	}
	if err := c.issue(rule, ConversionResponse, route, "/model", "provider omitted the response model; the outbound request model was substituted and is not an observed provider model", sink, true); err != nil {
		return err
	}
	response.Model = origin.RequestModel
	return nil
}

// RepairCollectedResponseModel applies only the source-model repair selected by
// the collector's policy. Client envelopes and other response rules stay outside
// the collector, which returns provider semantics rather than client wire output.
func (c *CompiledConversion) RepairCollectedResponseModel(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	if c == nil {
		return response, nil
	}
	copy := *response
	for _, rule := range c.Policy.Rules {
		if !rule.Enabled || rule.Phase != ConversionResponse || rule.Action != "chat_response_model" || !rule.Match.matches(route, Value{}) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.repairChatResponseModel(&copy, route, rule, sink); err != nil {
			return nil, err
		}
	}
	return &copy, nil
}
