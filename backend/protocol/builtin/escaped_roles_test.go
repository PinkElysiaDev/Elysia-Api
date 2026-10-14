package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestWireContractEscapedRoles(t *testing.T) {
	cases := []struct {
		name, protocol, body string
		direction            p.Direction
		wantError            bool
	}{
		{"anthropic_normal", Anthropic, "{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}", p.EncodeRequest, false},
		{"anthropic_equivalent", Anthropic, "{\"model\":\"m\",\"messages\":[{\"role\":\"\\u0075ser\",\"content\":\"hello\"}]}", p.EncodeRequest, false},
		{"responses_invalid_role", Responses, "{\"model\":\"m\",\"input\":[{\"type\":\"message\",\"role\":\"invalid\",\"content\":\"hello\"}]}", p.EncodeRequest, true},
		{"responses_escaped_type_bypass", Responses, "{\"model\":\"m\",\"input\":[{\"type\":\"\\u006dessage\",\"role\":\"invalid\",\"content\":\"hello\"}]}", p.EncodeRequest, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			value, err := p.ParseValue([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			err = (module{name: tt.protocol}).ValidateWireOutput(tt.direction, value)
			if (err != nil) != tt.wantError {
				t.Fatalf("err=%v", err)
			}
			t.Logf("error: %v", err)
		})
	}
}
