package protocol

import (
	"reflect"
	"testing"
)

func TestGeneratedNormalizationMatchesOriginal(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `9007199254740993`, ` {"n":1e100,"n":2} `, `"<>&\u2028"`, "\"\xff\""} {
		payload := parseTestValue(t, raw)
		for kind, plan := range typedLimitPlans {
			value := reflect.New(kind).Elem()
			for _, shouldFill := range []bool{false, true} {
				if shouldFill {
					fillMeterValue(value, payload, 0)
				}
				want, got := cloneSemantic(value, plan), cloneSemantic(value, plan)
				before, after := semanticCache{limits: DefaultLimits()}, semanticCache{limits: DefaultLimits()}
				before.originalNormalizeDocument(want, plan)
				after.normalizeDocument(got, plan)
				if !reflect.DeepEqual(got.Interface(), want.Interface()) {
					t.Fatalf("normalization changed %s (filled %t)", kind, shouldFill)
				}
			}
		}
	}
}

func TestGeneratedNormalizationKeepsEmptyAndPrivateFields(t *testing.T) {
	for _, request := range []*Request{
		{Content: []Node{}, Tools: []Tool{}, Parameters: Object{}, ClientOutput: &ClientOutput{IncludeUsage: new(bool)}},
		{Content: []Node{{ReasoningContent: []Node{}, Children: []Node{}, Attributes: Object{}}}},
		{Parameters: Object{"\xff": StringValue("invalid one"), "\xfe": StringValue("invalid two"), "\ufffd": StringValue("valid"), "absent": {}}, continuationsAccepted: true, continuationInput: &continuationInput{}},
	} {
		value := reflect.ValueOf(request)
		plan := typedLimitPlans[value.Type()]
		want, got := cloneSemantic(value, plan), cloneSemantic(value, plan)
		before, after := semanticCache{limits: DefaultLimits()}, semanticCache{limits: DefaultLimits()}
		before.originalNormalizeDocument(want, plan)
		after.normalizeDocument(got, plan)
		if !reflect.DeepEqual(want.Interface(), got.Interface()) {
			t.Fatal("normalization changed empty collections, key collisions or host fields")
		}
	}
}
