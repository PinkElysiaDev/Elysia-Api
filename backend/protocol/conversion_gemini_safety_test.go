package protocol

import "testing"

func TestGeminiSafetySettingsRuleValidation(t *testing.T) {
	base := ConversionRule{ID: "safety", Enabled: true, Order: 170, Phase: ConversionRequest, Action: "gemini_safety_settings", Value: StringValue("responses")}
	for _, tc := range []struct {
		name   string
		change func(*ConversionRule)
	}{
		{"phase", func(r *ConversionRule) { r.Phase = ConversionResponse }},
		{"node", func(r *ConversionRule) { r.Match.NodeKind = TextNode }},
		{"codec", func(r *ConversionRule) { r.Value = StringValue("unknown") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := base
			tc.change(&rule)
			if _, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "safety", Rules: []ConversionRule{rule}}); err == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	if _, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "safety", Rules: []ConversionRule{base}}); err != nil {
		t.Fatal(err)
	}
}
