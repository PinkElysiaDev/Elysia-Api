package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestContinuationExtractionNoChangePreservesWire(t *testing.T) {
	for _, name := range []string{Chat, Responses, Anthropic} {
		t.Run(name, func(t *testing.T) {
			compiled := testCompiled(t, name)
			key := "messages"
			if name == Responses {
				key = "input"
			}
			body := `{ "` + key + `": [{ "role": "user", "content": "` + p.ContinuationPrefix + strings.Repeat("A", 64<<10) + `" }], "n": 9007199254740993 }`
			if name != Responses {
				if _, _, err := compiled.ExtractContinuations(t.Context(), testValue(t, body), p.EvaluationContext{}); err == nil {
					t.Fatal("carrier extraction bypassed the source request contract")
				}
				body = strings.Replace(body, `{ `, `{ "model":"m", `, 1)
				if name == Chat {
					body = strings.Replace(body, `"n":`, `"vendor_count":`, 1)
				}
			}
			input := testValue(t, body)
			options, tokens, err := compiled.ExtractContinuations(t.Context(), input, p.EvaluationContext{})
			if err != nil || len(tokens) != 0 {
				t.Fatal("carrier-like user text became reserved state", tokens, err)
			}
			request, err := compiled.DecodeRequestValue(t.Context(), input, options)
			if err != nil || request.Native == nil || request.Native.Value != input || input.JSONString() != body {
				t.Fatal("unchanged extraction rebuilt source evidence", err)
			}
		})
	}
}

func TestContinuationExtractionRebuildsOnlyChangedHistory(t *testing.T) {
	compiled := testCompiled(t, Chat)
	token := p.ContinuationPrefix + "opaque"
	body := `{"model":"m","messages":[{ "role": "user", "content": "hello", "n": 9007199254740993 },{"role":"assistant","content":"ok","elysia_continuation":["` + token + `"]}]}`
	input := testValue(t, body)
	options, tokens, err := compiled.ExtractContinuations(t.Context(), input, p.EvaluationContext{})
	if err != nil || len(tokens) != 1 || tokens[0] != token {
		t.Fatal("carrier extraction", tokens, err)
	}
	request, err := compiled.DecodeRequestValue(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.AcceptContinuations(tokens); err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequestValue(t.Context(), request, options)
	if err != nil || input.JSONString() != body {
		t.Fatal("carrier extraction changed immutable input", err)
	}
	fields, _ := wire.ReadObject()
	var messages []p.Object
	if err := fields["messages"].Decode(&messages); err != nil || len(messages) != 2 || messages[0]["n"].JSONString() != "9007199254740993" || !messages[0]["content"].IsString("hello") || !messages[1]["elysia_continuation"].IsZero() || !messages[1]["content"].IsString("ok") {
		t.Fatal("carrier removal changed unrelated history", wire, err)
	}
	invalid := testValue(t, `{"model":"m","messages":[{"role":"assistant","elysia_continuation":["invalid"]}]}`)
	if _, _, err := compiled.ExtractContinuations(t.Context(), invalid, p.EvaluationContext{}); err == nil {
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
