package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const attributionFixture = `{"items":{"msg":{"input_tokens":2,"output_tokens":1,"cached_tokens":0,"cache_write_tokens":0,"content":[{"input_tokens":2,"output_tokens":1,"cached_tokens":0,"cache_write_tokens":0}]}},"request_fields":{"instructions":{"input_tokens":1,"output_tokens":0,"cached_tokens":1,"cache_write_tokens":0}}}`

func TestResponsesUsageAttributionProjectionAndSnapshots(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict"} {
			for _, enabled := range []bool{false, true} {
				policy := p.DefaultConversionPolicy(to, from)
				policy.Mode = mode
				for i := range policy.Rules {
					if policy.Rules[i].Action == "usage_projection" {
						policy.Rules[i].Enabled = enabled
					}
				}
				c, _ := p.ResolveConversion(policy)
				body := strings.Replace(hostedUsageResponse(`{}`), `"input_tokens":3`, `"attribution":`+attributionFixture+`,"input_tokens":3`, 1)
				// Keep this case focused on accounting loss rather than item identities.
				body = strings.Replace(body, `"id":"msg","status":"completed",`, "", 1)
				r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				if !equivalentJSON(r.Usage.Attribution, testValue(t, attributionFixture)) {
					t.Fatal(r.Usage)
				}
				sink := &p.DiagnosticSink{}
				projected, err := c.Response(t.Context(), r, p.ConversionContext{}, sink)
				var wire []byte
				if err == nil {
					projected.Native = nil
					wire, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
				}
				wantOK := target == Responses || mode == "compatible" && enabled
				if (err == nil) != wantOK {
					t.Fatalf("%s/%s/%t: %v", target, mode, enabled, err)
				}
				if !wantOK {
					if !strings.Contains(err.Error(), "attribution") {
						t.Fatal(err)
					}
					continue
				}
				if r.Usage.Attribution.IsZero() || r.Usage.Input.Count != 3 || r.Usage.Total.Count != 4 {
					t.Fatal("original accounting changed")
				}
				if target == Responses {
					if !strings.Contains(string(wire), `"attribution"`) {
						t.Fatal(string(wire))
					}
					continue
				}
				if !projected.Usage.Attribution.IsZero() {
					t.Fatal(projected.Usage)
				}
				found := false
				for _, i := range sink.Issues() {
					if i.Path == "/usage/attribution" {
						found = true
						if i.RuleID == "" || i.PolicyHash != c.Hash || i.Fidelity != "lossy_compatible" {
							t.Fatal(i)
						}
					}
				}
				if !found {
					t.Fatal("missing accounting projection diagnostic")
				}
			}
		}
		raw := responsesPaddedTextFrames()
		progress := `{"type":"response.in_progress","response":{"id":"r","status":"in_progress","output":[],"usage":{"attribution":` + attributionFixture + `}}}`
		raw = append(raw[:1], append([]string{progress, progress}, raw[1:]...)...)
		frames := auditConvertFrames(t, Responses, target, raw)
		found := false
		for _, f := range frames {
			if strings.Contains(string(f.Bytes()), `"attribution"`) {
				found = true
			}
		}
		if found != (target == Responses) {
			t.Fatal("stream attribution snapshot lost", target)
		}
	}
}

func TestUsageAttributionValidationEmptyAndUnknown(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"items":{},"request_fields":{}}`, attributionFixture, `{"items":{"large":{"input_tokens":9007199254740993}}}`} {
		known, err := p.ParseUsageAttribution(testValue(t, raw))
		if err != nil || !known {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{`42`, `{"items":null}`, `{"items":[]}`, `{"items":{"m":42}}`, `{"items":{"m":{"input_tokens":null}}}`, `{"items":{"m":{"input_tokens":-1}}}`, `{"items":{"m":{"input_tokens":0,"cached_tokens":1}}}`, `{"items":{"m":{"input_tokens":2,"cached_tokens":2,"cache_write_tokens":1}}}`, `{"items":{"m":{"output_tokens":1,"content":[{"output_tokens":2}]}}}`, `{"items":{"m":{"input_tokens":9223372036854775808}}}`} {
		if _, err := p.ParseUsageAttribution(testValue(t, raw)); err == nil {
			t.Fatal("invalid attribution accepted", raw)
		}
	}
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, raw := range []string{`{"vendor":true}`, `{"items":{"m":{"input_tokens":1,"vendor":false}}}`} {
		body := `{"id":"r","output":[],"usage":{"attribution":` + raw + `}}`
		r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
		wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil || !strings.Contains(string(wire), `"vendor"`) {
			t.Fatal(string(wire), err)
		}
		projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err == nil {
			_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("unknown attribution discarded")
		}
		frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.in_progress","response":`+body+`}`), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		if len(frame.Events) == 0 || frame.Events[0].Unmapped == nil {
			t.Fatal("unknown in-progress usage disappeared")
		}
	}
}

func TestEmptyAttributionIsNormalizedInStrictMode(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	policy := p.DefaultConversionPolicy(to, from)
	policy.Mode = "strict"
	c, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `{"items":{},"request_fields":{}}`} {
		source := &p.Response{SchemaVersion: p.SemanticSchemaVersion, Usage: &p.Usage{Attribution: testValue(t, raw)}}
		sink := &p.DiagnosticSink{}
		r, err := c.Response(t.Context(), source, p.ConversionContext{}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Usage.Attribution.IsZero() || source.Usage.Attribution.IsZero() {
			t.Fatal("empty projection mutated source or kept unsupported field")
		}
		found := false
		for _, i := range sink.Issues() {
			if i.Path == "/usage/attribution" {
				found = true
				if i.Code != p.ConversionNormalized || i.Fidelity != "preserved" || i.RuleID == "" {
					t.Fatal(i)
				}
			}
		}
		if !found {
			t.Fatal("empty attribution vanished without diagnostic")
		}
	}
}
