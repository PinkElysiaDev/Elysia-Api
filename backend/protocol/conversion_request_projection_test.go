package protocol

import "testing"

func TestRequestProjectionCodecOptions(t *testing.T) {
	for _, raw := range []string{`"responses"`, `{}`, `{"targetCodec":"unknown"}`, `{"sourceCodec":"unknown","targetCodec":"responses"}`} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		_, err = CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "projection", Mode: "compatible", Rules: []ConversionRule{{ID: "request-projection", Enabled: true, Phase: ConversionRequest, Action: "request_projection", Value: value}}})
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	value, _ := EncodeValue(requestProjectionOptions{TargetCodec: "responses"})
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "projection", Mode: "strict", Rules: []ConversionRule{{ID: "request-projection", Enabled: true, Phase: ConversionRequest, Action: "request_projection", Value: value}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Request(t.Context(), &Request{SchemaVersion: 1, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("f")}}}, ConversionContext{}, nil)
	if err != nil || !out.Tools[0].Options["strict"].IsZero() {
		t.Fatal("unknown source default inferred", out, err)
	}
}
