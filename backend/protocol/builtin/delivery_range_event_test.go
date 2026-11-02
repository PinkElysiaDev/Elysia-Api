package builtin

import (
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageEventDeliveryRanges(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	raw, err := os.ReadFile("testdata/responses-text-ranges.json")
	if err != nil {
		t.Fatal(err)
	}
	var values []p.Value
	if err := testValue(t, string(raw)).Decode(&values); err != nil {
		t.Fatal(err)
	}
	raws := make([]string, len(values))
	for index, value := range values {
		raws[index] = value.JSONString()
	}
	frames, _ := decodePackageStream(t, source, raws, p.Scope{})
	for _, name := range []string{"openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.SSE}
			converted := make([]*p.EventFrame, len(frames))
			for index, frame := range frames {
				converted[index] = &p.EventFrame{Events: make([]p.Event, len(frame.Events))}
				for eventIndex, event := range frame.Events {
					converted[index].Events[eventIndex], err = conversion.Event(t.Context(), event, route, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			wire := encodePackageStreamWithOptions(t, target, converted, (p.EvaluationContext{}).WithConversion(conversion, route), false)
			for _, rewrite := range []bool{false, true} {
				validation, err := target.NewWireStreamValidation(p.Scope{})
				if err != nil {
					t.Fatal(err)
				}
				defer validation.Release()
				for _, raw := range wire {
					before := testValue(t, raw)
					after := before
					if rewrite {
						// The corresponding done snapshots also carry the edit.
						// Rejection must come from the late citation, not a stale snapshot.
						after = testValue(t, strings.ReplaceAll(raw, `"hi"`, `"HI"`))
					}
					err = validation.ConsumeTransformed(t.Context(), before, after, p.EvaluationContext{})
					if err != nil {
						if !rewrite || !strings.Contains(err.Error(), "unproven text") || !strings.Contains(raw, "url_citation") {
							t.Fatal("failed outside the late reference boundary", raw, err)
						}
						break
					}
					if err = validation.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
				}
				if rewrite && err == nil {
					t.Fatal("late reference accepted an equal-length rewrite")
				}
				if !rewrite {
					if err := validation.Finish(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
