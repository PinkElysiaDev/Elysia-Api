package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestNewAPIErrorProjectionMatrix(t *testing.T) {
	for _, source := range []string{Chat, Responses} {
		from := shippedProjectionProtocol(t, source)
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			for _, status := range []int{200, 400, 401, 429, 500} {
				for _, isStream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%d/stream=%v", source, target, status, isStream), func(t *testing.T) {
						conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
						if err != nil {
							t.Fatal(err)
						}
						raw := october14Fixture(t, "new_api_error")
						statusValue, _ := p.EncodeValue(status)
						options := p.EvaluationContext{State: p.NewEvaluationState(), Values: p.Object{"httpStatus": statusValue}}
						sink := &p.DiagnosticSink{}
						var wire []byte
						var failure p.Value
						if !isStream {
							var response *p.Response
							if status == 200 {
								response, err = from.DecodeResponse(t.Context(), raw.Bytes(), options)
							} else {
								response, err = from.DecodeHTTPFailure(t.Context(), status, raw.Bytes(), options)
							}
							if err != nil {
								t.Fatal(err)
							}
							failure = response.Error
							if p.CheckGenerationOutcome(response) == nil {
								t.Fatal("error envelope became success")
							}
							response, err = conversion.Response(t.Context(), response, p.ConversionContext{}, sink)
							if err == nil {
								wire, err = to.EncodeResponse(t.Context(), response, options)
							}
						} else {
							fields, _ := raw.ReadObject()
							if source == Responses {
								fields["type"] = p.StringValue("error")
							}
							frame, decodeErr := from.DecodeFrame(t.Context(), object(fields), options)
							if decodeErr != nil {
								t.Fatal(decodeErr)
							}
							if len(frame.Events) != 1 || frame.Events[0].Type != p.OperationFailed {
								t.Fatal(frame.Events)
							}
							failure = frame.Events[0].Error
							event, projectErr := conversion.Event(t.Context(), frame.Events[0], p.ConversionContext{}, sink)
							if projectErr != nil {
								t.Fatal(projectErr)
							}
							frames, encodeErr := to.EncodeFrames(t.Context(), event, options)
							err = encodeErr
							for _, output := range frames {
								wire = append(wire, output.Bytes()...)
							}
						}
						if err != nil {
							t.Fatal(err)
						}
						for _, detail := range []string{"new_api_error", "input_tokens_out_of_range", "Synthetic provider rejected the input"} {
							if !strings.Contains(string(wire), detail) {
								t.Fatal("lost provider detail", detail, string(wire))
							}
						}
						semantic, _ := failure.ReadObject()
						expected := ErrorClassUpstream
						if status != 200 {
							expected = ClassFromStatus(status)
						}
						if !semantic["category"].IsString(string(expected)) {
							t.Fatal("wrapper guessed category", status, string(failure.Bytes()))
						}
						if source == target && len(sink.Issues()) != 0 {
							t.Fatal("same codec degraded", sink.Issues())
						}
						if target == Chat || target == Responses {
							if !strings.Contains(string(wire), `"type":"new_api_error"`) {
								t.Fatal(string(wire))
							}
						} else if len(sink.Issues()) == 0 {
							t.Fatal("missing detail projection diagnostic")
						}
					})
				}
			}
		}
	}
}

func TestNewAPIErrorStrictDisabledAndUnknown(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	for _, mode := range []string{"strict", "disabled"} {
		policy := p.DefaultConversionPolicy(to, from)
		if mode == "strict" {
			policy.Mode = mode
		}
		for index := range policy.Rules {
			if mode == "disabled" && policy.Rules[index].Action == "error_metadata" {
				policy.Rules[index].Enabled = false
			}
		}
		conversion, _ := p.ResolveConversion(policy)
		response, err := from.DecodeHTTPFailure(t.Context(), 400, october14Fixture(t, "new_api_error").Bytes(), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		response, err = conversion.Response(t.Context(), response, p.ConversionContext{}, nil)
		if err == nil {
			_, err = to.EncodeResponse(t.Context(), response, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal(mode, "lost error structure silently")
		}
	}
	for _, payload := range []string{
		`{"type":"another_api_error","message":"synthetic"}`,
		`{"type":"new_api_error","message":"synthetic","vendor":{"private":"opaque"}}`,
		`{"type":"new_api_error","message":"synthetic","param":{}}`,
		`{"type":"new_api_error","message":"synthetic","code":42}`,
	} {
		response, err := from.DecodeHTTPFailure(t.Context(), 400, []byte(`{"error":`+payload+`}`), p.EvaluationContext{})
		if err == nil {
			conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			response, err = conversion.Response(t.Context(), response, p.ConversionContext{}, nil)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			}
		}
		if err == nil {
			t.Fatal("unknown detail accepted", payload)
		}
	}
}

func TestNewAPIErrorCodeOverridesWrapperStatus(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	for _, code := range []string{"invalid_api_key", "model_not_found", "gateway_concurrency_limit"} {
		response, err := compiled.DecodeHTTPFailure(t.Context(), 500, []byte(`{"error":{"type":"new_api_error","code":"`+code+`","param":"field","message":"synthetic"}}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := response.Error.ReadObject()
		expected := map[string]ErrorClass{"invalid_api_key": ErrorClassAuthentication, "model_not_found": ErrorClassModelNotFound, "gateway_concurrency_limit": ErrorClassRateLimit}[code]
		if !fields["category"].IsString(string(expected)) || !fields["code"].IsString(code) || !fields["param"].IsString("field") {
			t.Fatal(string(response.Error.Bytes()))
		}
	}
}
