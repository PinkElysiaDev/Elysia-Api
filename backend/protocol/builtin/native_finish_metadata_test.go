package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatProviderFinishMetadataProjection(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Responses)
	for _, value := range []string{`null`, `"stop"`, `42`} {
		wire := strings.Replace(auditResponses[Chat], `"finish_reason":`, `"native_finish_reason":`+value+`,"finish_reason":`, 1)
		r, err := from.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
		if value == `42` {
			if err == nil {
				t.Fatal("invalid metadata accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"compatible", "strict"} {
			policy := p.DefaultConversionPolicy(to, from)
			policy.Mode = mode
			c, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := c.Response(t.Context(), r, p.ConversionContext{}, sink)
			if mode == "strict" && value != `null` {
				if err == nil {
					t.Fatal("loss accepted in strict mode")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !sinkHas(sink, "native_finish_reason") {
				t.Fatal("missing diagnostic")
			}
			if _, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
		}
		encoded, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, encoded, wire)
	}
}

func TestChatProviderFinishMetadataOnToolOnlyStream(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	decode, encode := p.EvaluationContext{State: p.NewEvaluationState()}, p.EvaluationContext{State: p.NewEvaluationState()}
	wires := []string{
		`{"id":"r","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":null,"native_finish_reason":null}]}`,
		`{"id":"r","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls","native_finish_reason":"tool_calls"}]}`,
	}
	var output string
	for _, wire := range wires {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, wire), decode)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range frame.Events {
			if e.Item != nil && e.Item.Kind == p.TextNode {
				t.Fatal("metadata invented text")
			}
		}
		frames, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, encode)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range frames {
			output += string(f.Bytes())
		}
	}
	if !strings.Contains(output, `"native_finish_reason":"tool_calls"`) || !strings.Contains(output, `"native_finish_reason":null`) {
		t.Fatal("lost choice metadata", output)
	}
}
