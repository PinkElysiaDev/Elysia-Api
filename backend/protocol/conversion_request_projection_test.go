package protocol

import "testing"

func TestRequestProjectionRejectsCodecOptions(t *testing.T) {
	for _, raw := range []string{`"responses"`, `{}`, `{"targetCodec":"unknown"}`, `{"sourceCodec":"unknown","targetCodec":"responses"}`, `{"targetCodec":"responses"}`} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		_, err = CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "projection", Mode: "compatible", Rules: []ConversionRule{{ID: "request-projection", Enabled: true, Phase: ConversionRequest, Action: "request_projection", Value: value}}})
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
