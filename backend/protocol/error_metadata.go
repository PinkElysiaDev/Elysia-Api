package protocol

import (
	"strconv"
	"strings"
)

type errorProjectionOptions struct {
	SourceCodec string `json:"sourceCodec"`
	TargetCodec string `json:"targetCodec"`
}

func (c *CompiledConversion) projectErrorMetadata(value Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	if value.IsZero() || value.IsNull() {
		return value, nil
	}
	var options errorProjectionOptions
	_ = rule.Value.Decode(&options)
	if options.SourceCodec == options.TargetCodec {
		return value, nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return Value{}, streamIssue(InvalidInput, "/error", "error projection requires an object")
	}
	var details []string
	for _, key := range []string{"providerType", "code", "param"} {
		field := fields[key]
		if field.IsZero() || field.IsNull() {
			continue
		}
		var text string
		wireKey := key
		if key == "providerType" {
			wireKey = "type"
		}
		if err := field.Decode(&text); err != nil {
			return Value{}, streamIssue(InvalidInput, "/error/"+wireKey, "provider error detail requires a string or null")
		}
		if key == "providerType" && text != "new_api_error" {
			return Value{}, streamIssue(UnsupportedCapability, "/error/type", "provider wrapper type has no declared projection")
		}
		if key == "param" && text == "" {
			delete(fields, key)
			c.normalized(rule, route, "/error/param", "empty error parameter carries no field detail", sink)
			continue
		}
		if options.TargetCodec == "openai-chat" || options.TargetCodec == "responses" {
			continue
		}
		if err := c.issue(rule, rule.Phase, route, "/error/"+wireKey, "target has no structured error detail field; preserved in the error message", sink, true); err != nil {
			return Value{}, err
		}
		details = append(details, wireKey+"="+strconv.Quote(text))
		delete(fields, key)
	}
	if len(details) > 0 {
		var message string
		if fields["message"].IsNull() || fields["message"].Decode(&message) != nil {
			return Value{}, streamIssue(InvalidInput, "/error/message", "provider error message requires a string")
		}
		fields["message"] = StringValue(message + " [upstream " + strings.Join(details, "; ") + "]")
	}
	return EncodeValue(fields)
}

func (c *CompiledConversion) errorMetadataValue(phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	if phase == ConversionResponse {
		var response Response
		if err := input.Decode(&response); err != nil {
			return Value{}, err
		}
		var err error
		response.Error, err = c.projectErrorMetadata(response.Error, route, rule, sink)
		if err != nil {
			return Value{}, err
		}
		return EncodeValue(response)
	}
	var event Event
	if err := input.Decode(&event); err != nil {
		return Value{}, err
	}
	var err error
	event.Error, err = c.projectErrorMetadata(event.Error, route, rule, sink)
	if err != nil {
		return Value{}, err
	}
	if event.Response != nil {
		event.Response.Error, err = c.projectErrorMetadata(event.Response.Error, route, rule, sink)
		if err != nil {
			return Value{}, err
		}
	}
	return EncodeValue(event)
}
