package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const cacheBillingFixture = `{"basis":"disclosed_billing_allocation","billed_cached_tokens":1,"observed_cached_tokens":3,"policy_revision":2,"rule_id":"test-rule"}`

func TestResponsesCacheBillingPreservedSeparatelyAndProjected(t *testing.T) {
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
				r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"input_tokens_details":{"cached_tokens":1},"linapi_cache_billing":`+cacheBillingFixture+`}}`), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				if r.Usage.CacheRead.Count != 1 || r.Usage.Total.Count != 4 || !equivalentJSON(r.Usage.CacheBilling, testValue(t, cacheBillingFixture)) {
					t.Fatal(r.Usage)
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
					t.Fatalf("%s/%s/%t: %v", target, mode, enabled, err)
				}
				if !wantOK {
					if !strings.Contains(err.Error(), "cacheBilling") {
						t.Fatal(err)
					}
					continue
				}
				if strings.Contains(string(wire), "linapi_cache_billing") != (target == Responses) {
					t.Fatal(string(wire))
				}
				if r.Usage.CacheBilling.IsZero() || r.Usage.CacheRead.Count != 1 {
					t.Fatal("projection polluted original accounting")
				}
				if target != Responses {
					found := false
					for _, i := range sink.Issues() {
						if i.Path == "/usage/cacheBilling" {
							found = true
							if i.RuleID == "" || i.PolicyHash != c.Hash || i.Fidelity != "lossy_compatible" {
								t.Fatal(i)
							}
						}
					}
					if !found {
						t.Fatal("no billing projection diagnostic")
					}
				}
			}
		}
		raw := responsesPaddedTextFrames()
		raw[len(raw)-1] = strings.Replace(raw[len(raw)-1], `"total_tokens":4`, `"total_tokens":4,"linapi_cache_billing":`+cacheBillingFixture, 1)
		frames := auditConvertFrames(t, Responses, target, raw)
		found := false
		for _, v := range frames {
			if strings.Contains(string(v.Bytes()), "linapi_cache_billing") {
				found = true
			}
		}
		if found != (target == Responses) {
			t.Fatal("stream billing disclosure lost", target)
		}
	}
}

func TestCacheBillingDoesNotGeneralizeUnknownVendorLedger(t *testing.T) {
	for _, raw := range []string{`42`, `{"billed_cached_tokens":null}`, `{"observed_cached_tokens":-1}`, `{"policy_revision":1.5}`, `{"rule_id":false}`} {
		if _, err := p.ParseCacheBilling(testValue(t, raw)); err == nil {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`{"billing":null}`, `{"basis":"new-policy"}`, strings.Replace(cacheBillingFixture, `"test-rule"`, `"test-rule","vendor":42`, 1)} {
		known, err := p.ParseCacheBilling(testValue(t, raw))
		if err != nil || known {
			t.Fatal("unknown shape consumed", raw, err)
		}
	}
	for _, raw := range []string{`null`, `{}`} {
		known, err := p.ParseCacheBilling(testValue(t, raw))
		if err != nil || !known {
			t.Fatal(raw, err)
		}
	}
}
