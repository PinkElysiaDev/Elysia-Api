package protocol

import (
	"strings"
	"testing"
)

func TestCanonicalUsageAccounting(t *testing.T) {
	type accountingCase struct {
		raw   string
		known bool
		path  string
	}
	for _, ledger := range []struct {
		name  string
		parse func(Value) (bool, error)
		cases []accountingCase
	}{
		{"attribution", ParseUsageAttribution, []accountingCase{
			{"", false, ""},
			{`null`, true, ""},
			{`{}`, true, ""},
			{`{"items":{},"requestFields":{}}`, true, ""},
			{`{"items":{"m":{"input":9223372036854775807,"cacheRead":9007199254740993,"content":[{"input":9007199254740993},{"input":1}]}}}`, true, ""},
			{`{"requestFields":{"q":{"input":4,"cacheRead":2,"cacheCreation":2}}}`, true, ""},
			{`{"request_fields":{}}`, false, ""},
			{`{"items":{"m":{"input_tokens":1}}}`, false, ""},
			{`{"items":{"m":{"content":[{"vendor":true}]}}}`, false, ""},
			{`{"requestFields":{"q":{"content":null}}}`, false, ""},
			{`42`, false, "/usage/attribution"},
			{`{"items":null}`, false, "/usage/attribution/items"},
			{`{"items":{"":{}}}`, false, "/usage/attribution/items"},
			{`{"items":{"a/b~c":{"input":-1}}}`, false, "/usage/attribution/items/a~1b~0c/input"},
			{`{"items":{"m":{"input":null}}}`, false, "/usage/attribution/items/m/input"},
			{`{"items":{"m":{"input":1.5}}}`, false, "/usage/attribution/items/m/input"},
			{`{"items":{"m":{"input":9223372036854775808}}}`, false, "/usage/attribution/items/m/input"},
			{`{"items":{"m":{"input":9223372036854775807,"cacheRead":9223372036854775807,"cacheCreation":1}}}`, false, "/usage/attribution/items/m/cacheCreation"},
			{`{"items":{"m":{"output":9223372036854775807,"content":[{"output":9223372036854775807},{"output":1}]}}}`, false, "/usage/attribution/items/m/content/1/output"},
			{`{"items":{"m":{"content":null}}}`, false, "/usage/attribution/items/m/content"},
			{`{"items":{"m":{"content":[{"input":0,"cacheRead":1}]}}}`, false, "/usage/attribution/items/m/content/0/cacheRead"},
		}},
		{"cache-billing", ParseCacheBilling, []accountingCase{
			{"", false, ""},
			{`null`, true, ""},
			{`{}`, true, ""},
			{`{"basis":"cache-allocation","billed":9007199254740993,"observed":1,"policyRevision":9223372036854775807,"ruleId":"rule"}`, true, ""},
			{`{"basis":"cache-allocation"}`, false, ""},
			{`{"basis":"new-policy","billed":1,"observed":2,"policyRevision":1,"ruleId":"rule"}`, false, ""},
			{`{"basis":"cache-allocation","billed":1,"observed":2,"policyRevision":1,"ruleId":"rule","vendor":true}`, false, ""},
			{`{"billed_cached_tokens":1}`, false, ""},
			{`42`, false, "/usage/cacheBilling"},
			{`{"billed":null}`, false, "/usage/cacheBilling/billed"},
			{`{"observed":-1}`, false, "/usage/cacheBilling/observed"},
			{`{"policyRevision":1.5}`, false, "/usage/cacheBilling/policyRevision"},
			{`{"billed":9223372036854775808}`, false, "/usage/cacheBilling/billed"},
			{`{"ruleId":false}`, false, "/usage/cacheBilling/ruleId"},
			{`{"basis":""}`, false, "/usage/cacheBilling/basis"},
		}},
	} {
		t.Run(ledger.name, func(t *testing.T) {
			for _, sample := range ledger.cases {
				t.Run(sample.raw, func(t *testing.T) {
					known, err := ledger.parse(Value{raw: sample.raw})
					if known != sample.known || (err != nil) != (sample.path != "") || err != nil && !strings.Contains(err.Error(), sample.path) {
						t.Fatalf("known=%t, error=%v; want known=%t, path=%q", known, err, sample.known, sample.path)
					}
				})
			}
		})
	}
}
