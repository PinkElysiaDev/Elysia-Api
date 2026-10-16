package protocol

import (
	"context"
	"fmt"
)

func (c *CompiledConversion) Request(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	if c != nil {
		if err := checkSemanticValue(request, c.phaseLimits(ctx, ConversionRequest)); err != nil {
			return nil, resourceIssue(err, route.Source, DecodeRequest, "conversion.request")
		}
	}
	v, err := EncodeSemantic(ctx, DecodeRequest, request)
	if err != nil {
		return nil, err
	}
	v, err = c.ApplyValue(ctx, ConversionIngress, v, route, sink)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionRequest, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Request
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	if c.Policy.Usage != nil {
		if result.ClientOutput == nil {
			result.ClientOutput = &ClientOutput{}
		}
		result.ClientOutput.CollectUsage = c.Policy.Usage.CollectUpstreamUsage
	}
	return &result, nil
}

func (c *CompiledConversion) Response(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	v, err := EncodeSemantic(ctx, DecodeResponse, response)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionResponse, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Response
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *CompiledConversion) Event(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (Event, error) {
	v, err := EncodeSemantic(ctx, DecodeEvent, event)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionEvent, v, route, sink)
	}
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = decodeContract(v.Bytes(), &result)
	return result, err
}

func (c *CompiledConversion) streamOptions(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	stream := false
	_ = request.Parameters["stream"].Decode(&stream)
	var codec string
	_ = rule.Value.Decode(&codec)
	for i := range request.Tools {
		tool := &request.Tools[i]
		value := tool.Options["eager_input_streaming"]
		if value.IsZero() {
			continue
		}
		at := fmt.Sprintf("/tools/%d/options/eager_input_streaming", i)
		if err := ValidateToolStreamOption(value, at); err != nil {
			return err
		}
		if stream && (codec == "" || codec == "anthropic") {
			continue
		}
		if !stream || value.IsNull() {
			c.normalized(rule, route, at, "tool input streaming preference has no effect on this request", sink)
		} else if err := c.issueSeverity(rule, ConversionRequest, route, at, "target cannot express per-tool input streaming timing; tool input and schema remain intact", sink, true, SeverityInfo); err != nil {
			return err
		}
		delete(tool.Options, "eager_input_streaming")
	}
	if raw, exists := request.Parameters["stream_options"]; exists && !raw.IsZero() {
		legacy, err := ParseClientOutput(raw)
		if err != nil {
			return err
		}
		if request.ClientOutput != nil && !request.ClientOutput.RawStreamOptions.IsZero() && !equalValues(request.ClientOutput.RawStreamOptions, raw) {
			return streamIssue(InvalidInput, "/stream_options", "conflicting legacy and typed client output preferences")
		}
		if request.ClientOutput != nil {
			legacy.CollectUsage = request.ClientOutput.CollectUsage
			legacy.RawResponsesInclude = request.ClientOutput.RawResponsesInclude
		}
		request.ClientOutput = legacy
		delete(request.Parameters, "stream_options")
	}
	output := request.ClientOutput
	if output == nil {
		if c.Policy.Usage == nil || !c.Policy.Usage.DefaultIncludeUsage {
			return nil
		}
		output = &ClientOutput{}
		request.ClientOutput = output
	}
	if output.IncludeUsage == nil && c.Policy.Usage != nil && c.Policy.Usage.DefaultIncludeUsage {
		v := true
		output.IncludeUsage = &v
	}
	if output.RawStreamOptions.IsZero() {
		return nil
	}
	if _, err := ParseClientOutput(output.RawStreamOptions); err != nil {
		return err
	}
	foreign := route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion
	fields, _ := output.RawStreamOptions.ReadObject()
	for _, key := range sortedKeys(fields) {
		if key != "include_usage" && foreign {
			reject := rule
			reject.Action = "reject"
			return candidateFailure(c.issue(reject, ConversionRequest, route, "/stream_options/"+escapePointer(key), "client stream extension requires an explicit target mapping", sink, false))
		}
	}
	if !stream {
		if _, exists := fields["include_usage"]; exists || output.RawStreamOptions.IsNull() || len(fields) == 0 {
			delete(fields, "include_usage")
			output.RawStreamOptions = Value{}
			if len(fields) > 0 {
				output.RawStreamOptions, _ = EncodeValue(fields)
			}
			c.normalized(rule, route, "/stream_options", "known stream options have no effect on a non-streaming request", sink)
		}
	} else if foreign {
		output.RawStreamOptions = Value{}
	}
	return nil
}
