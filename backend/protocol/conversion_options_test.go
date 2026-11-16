package protocol

import "testing"

func TestCompiledOptionsKeepOverridesAndErrorTiming(t *testing.T) {
	rule := ConversionRule{ID: "option", Order: 1, Enabled: true, Phase: ConversionRequest, Action: "set", Path: "/parameters/choice", Value: StringValue("first")}
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "options", Rules: []ConversionRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	c.options[rule.ID] = parseConversionOption[string](rule.Value)
	if value, err := readConversionOption[string](c, rule); err != nil || value != "first" {
		t.Fatal(value, err)
	}
	rule.Value = StringValue("second")
	if value, err := readConversionOption[string](c, rule); err != nil || value != "second" {
		t.Fatal("stale option", value, err)
	}
	rule.Value = Value{}
	if _, err := readConversionOption[string](c, rule); err == nil {
		t.Fatal("absent option error lost")
	}
	if _, err := readConversionOption[Object](c, c.Policy.Rules[0]); err == nil {
		t.Fatal("option type mismatch hidden")
	}
}
