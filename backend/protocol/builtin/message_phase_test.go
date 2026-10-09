package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesMessagePhaseJSONAndHistory(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, value := range []string{`null`, `"commentary"`, `"final_answer"`} {
		for _, content := range []string{`[]`, `[{"type":"output_text","text":"OK","annotations":[]}]`} {
			message := `{"id":"msg","type":"message","role":"assistant","status":"completed","phase":` + value + `,"content":` + content + `}`
			r, err := c.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[`+message+`]}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Content) != 1 || len(r.Content[0].Metadata) != 1 {
				t.Fatal("phase owner missing", r.Content)
			}
			// Force reconstruction, not original-body replay. The same item
			// must survive both client delivery and next-turn history.
			r.Native, r.Content[0].Native = nil, nil
			wire, err := c.EncodeResponse(t.Context(), r, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			obj, _ := testValue(t, string(wire)).ReadObject()
			items, _ := readArray(obj["output"])
			if len(items) != 1 {
				t.Fatal("message dropped", string(wire))
			}
			sameJSON(t, items[0].Bytes(), message)
			request, err := c.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[`+message+`]}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			request.Native, request.Content[0].Native = nil, nil
			wire, err = c.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			obj, _ = testValue(t, string(wire)).ReadObject()
			items, _ = readArray(obj["input"])
			if len(items) != 1 {
				t.Fatal("history message dropped", string(wire))
			}
			sameJSON(t, items[0].Bytes(), message)
		}
	}
	for _, value := range []string{`42`, `true`, `[]`, `{}`, `"unknown"`, `""`} {
		for _, direction := range []p.Direction{p.DecodeRequest, p.DecodeResponse} {
			body := `{"model":"m","input":[{"role":"assistant","phase":` + value + `,"content":[]}]}`
			var err error
			if direction == p.DecodeRequest {
				_, err = c.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
			} else {
				_, err = c.DecodeResponse(t.Context(), []byte(strings.Replace(body, `"input"`, `"output"`, 1)), p.EvaluationContext{})
			}
			if err == nil || !strings.Contains(err.Error(), "invalid_input") || !strings.Contains(err.Error(), "/0/phase") {
				t.Fatalf("bad phase %s: %v", value, err)
			}
		}
	}
}

func TestResponsesMessagePhaseUsesNamedProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, codec := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict"} {
			for _, value := range []string{`null`, `"commentary"`, `"final_answer"`} {
				t.Run(codec+mode+value, func(t *testing.T) {
					r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"role":"assistant","phase":`+value+`,"content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					policy := p.DefaultConversionPolicy(to, from)
					policy.Mode = mode
					conversion, _ := p.ResolveConversion(policy)
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Response(t.Context(), r, p.ConversionContext{}, sink)
					if mode == "strict" && value != `null` {
						if err == nil || !strings.Contains(err.Error(), "/phase") {
							t.Fatal("phase loss accepted in strict mode", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err != nil {
						t.Fatal(err)
					}
					if !sinkHas(sink, "/phase") {
						t.Fatal("phase omission has no diagnostic")
					}
					for i := range policy.Rules {
						if policy.Rules[i].Action == "response_metadata" {
							policy.Rules[i].Enabled = false
						}
					}
					conversion, _ = p.ResolveConversion(policy)
					projected, err = conversion.Response(t.Context(), r, p.ConversionContext{}, nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err == nil {
						t.Fatal("encoder silently omitted phase with rule disabled")
					}
				})
			}
		}
	}
}

func TestResponsesMessagePhaseOldCustomRequestProjection(t *testing.T) {
	definition := shippedProjectionProtocol(t, Responses).Definition()
	definition.ID, definition.Requires = "old-custom-responses", nil
	for direction, mapping := range definition.Directions {
		mapping.After = &p.Expression{Op: "read"}
		definition.Directions[direction] = mapping
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(definition)
	from, issues := compiler.Compile(raw.Bytes())
	if from == nil {
		t.Fatal(issues)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			request, err := from.DecodeRequest(t.Context(), []byte(`{"model":"m","max_output_tokens":64,"store":false,"input":[{"role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"OK"}]}]}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			policy := p.DefaultConversionPolicy(from, to)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].Action == "response_metadata" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{}, sink)
			if target != Responses && mode == "strict" {
				if err == nil || !strings.Contains(err.Error(), "/phase") {
					t.Fatal(target, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if mode == "disabled" && target != Responses {
				if err == nil || !strings.Contains(err.Error(), "/phase") {
					t.Fatal("request ignored disabled rule", target, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(target, err)
			}
			kept := strings.Contains(string(wire), `"phase":"commentary"`)
			if kept != (target == Responses) {
				t.Fatal(target, string(wire))
			}
			if target != Responses && !sinkHas(sink, "/phase") {
				t.Fatal("history phase loss lacks diagnostic")
			}
		}
	}
}

func TestResponsesMessagePhaseCombinationEvidence(t *testing.T) {
	definition := shippedProjectionProtocol(t, Responses).Definition()
	definition.Samples = append(definition.Samples, p.Sample{
		ID: "message-phase-evidence", Direction: p.DecodeResponse,
		Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"OK","annotations":[]}]}]}`),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"id":"r","model":"m","status":"completed","attributes":{"finishReason":"stop","created_at":1},"content":[{"kind":"message","id":"msg","status":"completed","role":"assistant","metadata":[{"codec":"responses","sourceCodec":"responses","location":"message","name":"phase","path":"/output/0/phase","value":"commentary"}],"children":[{"kind":"text","payload":"OK"}]}]}`),
	})
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(definition)
	upstream, issues := compiler.Compile(raw.Bytes())
	if upstream == nil {
		t.Fatal(issues)
	}
	if report := p.Verify(t.Context(), upstream); !report.Passed {
		t.Fatal("independent oracle invalid", report.Issues)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			policy := p.DefaultConversionPolicy(to, upstream)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].Action == "response_metadata" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			report := p.VerifyBindingCombination(t.Context(), to, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, conversion)
			found := false
			for _, check := range report.Checks {
				if check.SampleID == "message-phase-evidence" {
					found = true
					if check.Skipped || check.Passed != (target == Responses || mode == "compatible") {
						t.Fatal(target, mode, check, report.Issues)
					}
				}
			}
			if !found {
				t.Fatal("phase evidence not executed")
			}
		}
	}
}
