package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestContinuationValueParityAndOwnership(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"` + strings.Repeat("A", 128<<10) + `"}],"metadata":{"session_id":"owned"}}`,
		`{"messages":[{"role":"assistant","content":"hello","elysia_continuation":["` + p.ContinuationPrefix + `token"]}]}`,
		`{"messages":[{"role":"assistant","elysia_continuation":["invalid"]}]}`,
	} {
		input := testValue(t, body)
		before, a, beforeErr := ExtractContinuationCarriers([]byte(body), "openai_chat", len(body))
		after, b, afterErr := ExtractContinuationCarriersValue(input, "openai_chat", len(body))
		if fmt.Sprint(beforeErr) != fmt.Sprint(afterErr) || strings.Join(a, "|") != strings.Join(b, "|") || string(before) != after.JSONString() {
			t.Fatalf("carrier value parity: %v / %v", beforeErr, afterErr)
		}
		if afterErr == nil && len(b) == 0 && input != after {
			t.Fatal("unchanged document was rebuilt")
		}
		if input.JSONString() != body {
			t.Fatal("carrier extraction mutated immutable input")
		}
	}
}
