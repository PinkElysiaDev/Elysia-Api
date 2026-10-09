package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiTerminalEmptyTextDoesNotInventHistoryItem(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	for _, wire := range []string{
		`{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"echo","args":{"value":7}}}]}}]}`,
		`{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`,
	} {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, wire), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if event.Item != nil && event.Item.Kind == p.TextNode {
				t.Fatal("terminal placeholder became a new history item")
			}
		}
	}
	for _, ending := range []string{`{"text":""}`, `{"text":"","thoughtSignature":"synthetic"}`} {
		compiled := testCompiled(t, Gemini)
		wire := `{"candidates":[{"content":{"role":"model","parts":[` + ending + `]}`
		if strings.Contains(ending, "Signature") {
			wire += `,"finishReason":"STOP"`
		}
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, wire+`}]}`), p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range frame.Events {
			if e.Item != nil {
				found = true
			}
		}
		if !found {
			t.Fatal("unclosed or signed empty part lost identity")
		}
	}
}
