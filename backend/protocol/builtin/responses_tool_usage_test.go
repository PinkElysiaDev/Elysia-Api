package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const hostedUsageFixture = `{"image_gen":{"input_tokens":7,"output_tokens":5,"total_tokens":12,"input_tokens_details":{"image_tokens":4,"text_tokens":3},"output_tokens_details":{"image_tokens":5,"text_tokens":0}},"web_search":{"num_requests":2}}`

func hostedUsageResponse(value string) string {
	return `{"id":"r","model":"m","status":"completed","output":[{"type":"message","id":"msg","status":"completed","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4},"tool_usage":` + value + `}`
}

func TestResponsesHostedUsageProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict"} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", target, mode, enabled), func(t *testing.T) {
					to := shippedProjectionProtocol(t, target)
					policy := p.DefaultConversionPolicy(to, from)
					policy.Mode = mode
					for i := range policy.Rules {
						if policy.Rules[i].Action == "usage_projection" {
							policy.Rules[i].Enabled = enabled
						}
					}
					c, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					r, err := from.DecodeResponse(t.Context(), []byte(hostedUsageResponse(hostedUsageFixture)), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					if len(r.Usage.Details) != 8 || r.Usage.Input.Count != 3 || r.Usage.Output.Count != 1 || r.Usage.Total.Count != 4 {
						t.Fatalf("tool ledger polluted model totals: %+v", r.Usage)
					}
					sink := &p.DiagnosticSink{}
					projected, err := c.Response(t.Context(), r, p.ConversionContext{}, sink)
					var wire []byte
					if err == nil {
						projected.Native = nil
						wire, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
					}
					wantOK := target == Responses || enabled && mode == "compatible"
					if (err == nil) != wantOK {
						t.Fatal(err)
					}
					if len(r.Usage.Details) != 8 {
						t.Fatal("source accounting mutated")
					}
					if !wantOK {
						return
					}
					if target == Responses {
						fields, _ := testValue(t, string(wire)).ReadObject()
						if !equivalentJSON(fields["tool_usage"], testValue(t, hostedUsageFixture)) {
							t.Fatalf("native ledger changed: %s", wire)
						}
					} else {
						if strings.Contains(string(wire), "tool_usage") || len(projected.Usage.Details) != 0 {
							t.Fatal(string(wire))
						}
						n := 0
						for _, issue := range sink.Issues() {
							if strings.Contains(issue.Path, "/details/tools.") {
								n++
								if issue.RuleID == "" || issue.PolicyHash != c.Hash || issue.Fidelity != "lossy_compatible" {
									t.Fatal(issue)
								}
							}
						}
						if n != 8 {
							t.Fatalf("expected all 8 count losses including zero, got %d", n)
						}
					}
				})
			}
		}
	}
}

func TestResponsesHostedUsageStreamSnapshots(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	created := `{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"status":"in_progress","output":[],"usage":{"input_tokens":3,"output_tokens":0}}}`
	progress := `{"type":"response.in_progress","response":{"id":"r","model":"m","status":"in_progress","output":[],"tool_usage":` + hostedUsageFixture + `}}`
	terminal := `{"type":"response.completed","response":` + hostedUsageResponse(hostedUsageFixture) + `}`
	raw := []string{created, progress, progress,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"OK"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"OK"}}`,
		terminal}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	var original *p.Usage
	for _, body := range raw {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			original = p.MergeUsage(original, event.Usage)
			if event.Response != nil {
				original = p.MergeUsage(original, event.Response.Usage)
			}
		}
	}
	if original.Details["tools.web_search_calls"].Count != 2 || original.Details["tools.image_generation.total_tokens"].Count != 12 || original.Total.Count != 4 {
		t.Fatal(original)
	}
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		frames := auditConvertFrames(t, Responses, target, raw)
		var all string
		for _, v := range frames {
			all += string(v.Bytes())
		}
		if !strings.Contains(all, "OK") {
			t.Fatal(all)
		}
		if target == Responses && !strings.Contains(all, `"tool_usage"`) {
			t.Fatal(all)
		}
		if target != Responses && strings.Contains(all, `"tool_usage"`) {
			t.Fatal(all)
		}
	}
}

func TestResponsesHostedUsageEmptyInvalidAndUnknown(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, value := range []string{`null`, `{}`, `{"image_gen":{},"web_search":{}}`, `{"image_gen":{"input_tokens_details":{}}}`} {
		r, err := from.DecodeResponse(t.Context(), []byte(hostedUsageResponse(value)), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
		wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := testValue(t, string(wire)).ReadObject()
		if !equivalentJSON(fields["tool_usage"], testValue(t, value)) {
			t.Fatal(string(wire))
		}
		projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{`42`, `[]`, `{"web_search":null}`, `{"web_search":{"num_requests":null}}`, `{"web_search":{"num_requests":-1}}`, `{"web_search":{"num_requests":1.5}}`, `{"web_search":{"num_requests":9223372036854775808}}`, `{"image_gen":{"input_tokens":2,"output_tokens":3,"total_tokens":4}}`, `{"image_gen":{"input_tokens":1,"input_tokens_details":{"image_tokens":2}}}`} {
		if _, err := from.DecodeResponse(t.Context(), []byte(hostedUsageResponse(value)), p.EvaluationContext{}); err == nil {
			t.Fatalf("invalid tool count accepted: %s", value)
		}
	}
	for _, value := range []string{`{"vendor":0}`, `{"image_gen":{"vendor":0}}`, `{"web_search":{"num_requests":2,"vendor":0}}`} {
		r, err := from.DecodeResponse(t.Context(), []byte(hostedUsageResponse(value)), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
		wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := testValue(t, string(wire)).ReadObject()
		if !equivalentJSON(fields["tool_usage"], testValue(t, value)) {
			t.Fatal(string(wire))
		}
		projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err == nil {
			t.Fatal("unknown tool usage extension discarded")
		}
	}
}

func TestResponsesHostedUsageRetainsIntegerPrecision(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	const count = int64(9007199254740993)
	r, err := from.DecodeResponse(t.Context(), []byte(hostedUsageResponse(`{"web_search":{"num_requests":9007199254740993}}`)), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Usage.Details["tools.web_search_calls"].Count != count {
		t.Fatal(r.Usage)
	}
	r.Native = nil
	wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(wire), `"num_requests":9007199254740993`) {
		t.Fatal(string(wire), err)
	}
}

func equivalentJSON(a, b p.Value) bool {
	var left, right any
	_ = a.Decode(&left)
	_ = b.Decode(&right)
	x, _ := p.EncodeValue(left)
	y, _ := p.EncodeValue(right)
	return string(x.Bytes()) == string(y.Bytes())
}
