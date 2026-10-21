package protocol

import (
	"context"
	"fmt"
	"reflect"
)

func (c *CompiledConversion) Request(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	if c != nil {
		if err := checkSemanticValue(request, c.phaseLimits(ctx, ConversionRequest)); err != nil {
			return nil, resourceIssue(err, route.Source, DecodeRequest, "conversion.request")
		}
	}
	if request != nil && c.canOwnRequest() {
		return c.convertOwnedRequest(ctx, request, route, sink)
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

// JSON-dependent authored rules retain the value boundary. This selection is
// made before executing any rule, so errors never cause a second execution.
func (c *CompiledConversion) canOwnRequest() bool {
	if c == nil {
		return false
	}
	return !c.HasPhase(ConversionIngress) && c.canOwnPhase(ConversionRequest)
}

func (c *CompiledConversion) convertOwnedRequest(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	plan := typedLimitPlans[reflect.TypeFor[*Request]()]
	owned := cloneSemantic(reflect.ValueOf(request), plan).Interface().(*Request)
	limits := c.phaseLimits(ctx, ConversionRequest)
	state := conversionState{document: owned, limits: limits, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: limits}}
	state.normalize(reflect.ValueOf(owned), plan)
	state.requestNormalized = true
	if _, err := c.applyConversionState(ctx, ConversionIngress, Value{}, route, sink, &state, false); err != nil {
		return nil, err
	}
	if _, err := c.applyConversionState(ctx, ConversionRequest, Value{}, route, sink, &state, false); err != nil {
		return nil, err
	}
	if c.Policy.Usage != nil {
		if owned.ClientOutput == nil {
			owned.ClientOutput = &ClientOutput{}
		}
		owned.ClientOutput.CollectUsage = c.Policy.Usage.CollectUpstreamUsage
	}
	return owned, nil
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
	if c != nil && c.canOwnPhase(ConversionEvent) {
		budget := semanticLimits(DefaultLimits())
		if err := checkSemanticValue(event, budget); err != nil {
			return Event{}, err
		}
		owned := normalizeMappingSemantic(&event, budget)
		state := conversionState{document: owned, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: budget}}
		if _, err := c.applyConversionState(ctx, ConversionEvent, Value{}, route, sink, &state, false); err != nil {
			return Event{}, err
		}
		return *owned, nil
	}
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

func (c *CompiledConversion) canOwnPhase(phase ConversionPhase) bool {
	return c.ownedPhases[phase]
}

func (c *CompiledConversion) streamOptions(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	stream := false
	_ = request.Parameters["stream"].Decode(&stream)
	codec, _ := readConversionOption[string](c, rule)
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
