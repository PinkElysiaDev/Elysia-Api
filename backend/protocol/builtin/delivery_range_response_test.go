package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func deliveryRangeResponse(t *testing.T, name, text string, start, end int, hasRange bool) p.Value {
	t.Helper()
	annotations := "[]"
	if hasRange {
		if name == "openai-chat" {
			annotations = fmt.Sprintf(`[{"type":"url_citation","url_citation":{"url":"https://example.test","title":"reference","start_index":%d,"end_index":%d}}]`, start, end)
		} else {
			annotations = fmt.Sprintf(`[{"type":"url_citation","url":"https://example.test","title":"reference","start_index":%d,"end_index":%d}]`, start, end)
		}
	}
	if name == "openai-chat" {
		return testValue(t, fmt.Sprintf(`{"id":"r","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%q,"annotations":%s},"finish_reason":"stop"}]}`, text, annotations))
	}
	return testValue(t, fmt.Sprintf(`{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":%s}]}],"output_text":%q}`, text, annotations, text))
}

func TestPackageResponseDeliveryRanges(t *testing.T) {
	for _, name := range []string{"openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			for _, test := range []struct {
				name, text                     string
				start, end, wantStart, wantEnd int
				valid, hasRange                bool
			}{
				{"identity", "😀abCDxy", 4, 6, 4, 6, true, true},
				{"equal-length-rewrite", "😀abEFxy", 4, 6, 4, 6, false, true},
				{"prefix", "😀abQxy", 0, 4, 0, 4, true, true},
				{"suffix-adjusted", "😀abQxy", 6, 8, 5, 7, true, true},
				{"suffix-old-indices", "😀abQxy", 6, 8, 6, 8, false, true},
				{"cross-rewrite", "😀abQxy", 3, 7, 3, 6, false, true},
				{"without-reference", "different body", 0, 0, 0, 0, true, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := deliveryRangeResponse(t, name, "😀abCDxy", test.start, test.end, test.hasRange)
					after := deliveryRangeResponse(t, name, test.text, test.wantStart, test.wantEnd, test.hasRange)
					err := compiled.ValidateResponseOutput(t.Context(), before, after, p.EvaluationContext{})
					if (err == nil) != test.valid {
						t.Fatal("unexpected final range check", err)
					}
					if !test.valid && !strings.Contains(err.Error(), "reference") {
						t.Fatal("failed outside the range boundary", err)
					}
				})
			}
			before := deliveryRangeResponse(t, name, "😀abCDxy", 4, 6, true)
			after := testValue(t, strings.Replace(before.JSONString(), `"model":"m"`, `"model":"alias"`, 1))
			if err := compiled.ValidateResponseOutput(t.Context(), before, after, p.EvaluationContext{}); err != nil {
				t.Fatal("unrelated edit invalidated references", err)
			}
		})
	}
}
