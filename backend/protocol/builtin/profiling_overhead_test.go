package builtin

import (
	"bytes"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestContinuationExtractionNoChangePreservesWire(t *testing.T) {
	for _, family := range []string{"openai_chat", "openai_responses", "claude"} {
		t.Run(family, func(t *testing.T) {
			key := "messages"
			if family == "openai_responses" {
				key = "input"
			}
			// Carrier-like text inside user content is not a reserved history carrier.
			body := []byte(`{ "` + key + `": [{ "role": "user", "content": "` + p.ContinuationPrefix + strings.Repeat("A", 64<<10) + `" }], "n": 9007199254740993 }`)
			got, tokens, err := ExtractContinuationCarriers(body, family, len(body))
			if err != nil || len(tokens) != 0 || !bytes.Equal(got, body) || &got[0] != &body[0] {
				t.Fatalf("no-change extraction rebuilt or changed the wire: %d tokens, %v", len(tokens), err)
			}
		})
	}
}

func TestContinuationExtractionRebuildsOnlyChangedHistory(t *testing.T) {
	token := p.ContinuationPrefix + "opaque"
	untouched := `{ "role": "user", "content": "hello", "n": 9007199254740993 }`
	body := []byte(`{"messages":[` + untouched + `,{"role":"assistant","content":"ok","elysia_continuation":["` + token + `"]}]}`)
	input := testValue(t, string(body))
	root, tokens, err := ExtractContinuationCarriersValue(input, "openai_chat", len(body))
	if err != nil || len(tokens) != 1 || tokens[0] != token {
		t.Fatalf("carrier extraction: %q, %v", tokens, err)
	}
	if input.JSONString() != string(body) {
		t.Fatal("carrier extraction mutated immutable input")
	}
	fields, _ := root.ReadObject()
	var messages []p.Value
	if err := fields["messages"].Decode(&messages); err != nil || len(messages) != 2 {
		t.Fatalf("history: %v", err)
	}
	if !strings.Contains(string(messages[0].Bytes()), "9007199254740993") {
		t.Fatal("unmodified history lost numeric precision")
	}
	assistant, _ := messages[1].ReadObject()
	if !assistant["elysia_continuation"].IsZero() || !assistant["content"].IsString("ok") {
		t.Fatal("carrier removal changed assistant content")
	}
	if _, _, err := ExtractContinuationCarriers([]byte(`{"messages":[{"role":"assistant","elysia_continuation":["invalid"]}]}`), "openai_chat", 1024); err == nil {
		t.Fatal("invalid reserved carrier accepted")
	}
}

func TestCollectUnknownPreservesExtensions(t *testing.T) {
	fields := p.Object{"id": p.StringValue("id"), "model": p.StringValue("model")}
	known := []string{"id", "model"}
	if !collectUnknown(fields, known).IsZero() {
		t.Fatal("known fields reported as unknown")
	}
	fields["extension"] = p.StringValue("preserved")
	extra, err := collectUnknown(fields, known).ReadObject()
	if err != nil || len(extra) != 1 || !extra["extension"].IsString("preserved") || len(fields) != 3 {
		t.Fatalf("extension collection mutated input or lost fields: %+v, %v", extra, err)
	}
}
