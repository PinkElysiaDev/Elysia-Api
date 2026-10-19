package protocol

import "testing"

func TestCompiledOptionsKeepOverridesAndErrorTiming(t *testing.T) {
	rule := ConversionRule{ID: "option", Order: 1, Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic")}
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "options", Rules: []ConversionRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := readConversionOption[string](c, rule); err != nil || value != "anthropic" {
		t.Fatal(value, err)
	}
	rule.Value = StringValue("responses")
	if value, err := readConversionOption[string](c, rule); err != nil || value != "responses" {
		t.Fatal("stale option", value, err)
	}
	rule.Value = Value{}
	if _, err := readConversionOption[string](c, rule); err == nil {
		t.Fatal("absent option error lost")
	}
	if _, err := readConversionOption[reasoningOptions](c, c.Policy.Rules[0]); err == nil {
		t.Fatal("option type mismatch hidden")
	}
}
