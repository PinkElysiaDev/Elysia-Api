package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageDuplicateSourceStarts(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, item := range []string{
		`{"type":"message","id":"m","role":"assistant","status":"in_progress","content":[]}`,
		`{"type":"function_call","id":"f","call_id":"c","name":"run","arguments":""}`,
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		wire, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		start := testValue(t, `{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`)
		frame := testValue(t, `{"type":"response.output_item.added","output_index":0,"item":`+item+`}`)
		for _, initial := range []p.Value{start, frame} {
			if _, err := wire.DecodeFrame(t.Context(), initial, options); err != nil {
				t.Fatal(err)
			}
		}
		_, err = wire.DecodeFrame(t.Context(), frame, options)
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) == 0 {
			t.Fatal("duplicate source start was accepted", err)
		}
		issue := failure.Issues[0]
		if issue.Code != p.UpstreamContractViolation || issue.Direction != p.DecodeEvent || issue.Protocol != compiled.Identity() || issue.Path != "/output_index" {
			t.Fatal("duplicate start lost its entity-key attribution", issue)
		}
	}
}

func TestResponsesPackageTextSourceStream(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	phases := []string{`"commentary"`, `"final_answer"`, `null`}
	for _, timing := range []string{"start", "done", "terminal"} {
		for _, isRefusal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/refusal=%t", timing, isRefusal), func(t *testing.T) {
				values := phaseStreamFrames(t, timing, phases)
				rawFrames := make([]string, len(values))
				for index, frame := range values {
					raw := frame.JSONString()
					if isRefusal {
						raw = strings.ReplaceAll(raw, "response.output_text", "response.refusal")
						raw = strings.ReplaceAll(raw, `"type":"output_text"`, `"type":"refusal"`)
						raw = strings.ReplaceAll(raw, `"text":`, `"refusal":`)
					}
					rawFrames[index] = raw
				}
				frames, response := decodePackageStream(t, compiled, rawFrames, p.Scope{})
				assertResponsesMessagePhases(t, response, phases, isRefusal)
				for _, frame := range frames {
					for _, event := range frame.Events {
						if event.Item == nil {
							continue
						}
						path := "/part"
						if event.Item.Kind == p.MessageNode {
							path = "/item"
						}
						if event.Item.Source == nil || event.Item.Source.Protocol != compiled.Identity() || event.Item.Source.Path != path {
							t.Fatal("stream item lost its original registered fragment", event.Item.Source)
						}
						if event.Item.Native != nil {
							t.Fatal("stream item retained a redundant native frame", event.Item.Kind)
						}
					}
				}
				// Rebuilding proves the mapping, independently of exact-frame replay.
				rendered := encodePackageStream(t, compiled, frames, p.Scope{}, false)
				_, rebuilt := decodePackageStream(t, compiled, rendered, p.Scope{})
				assertResponsesMessagePhases(t, rebuilt, phases, isRefusal)
			})
		}
	}
}

func assertResponsesMessagePhases(t *testing.T, response *p.Response, phases []string, isRefusal bool) {
	t.Helper()
	if response == nil || !response.ID.IsString("r") || !response.Model.IsString("m") || !response.Status.IsString("completed") || len(response.Content) != len(phases) {
		t.Fatal("message envelope or count changed", response)
	}
	counts := []int{1, 2, 0}
	for index, message := range response.Content {
		if message.Kind != p.MessageNode || !message.ID.IsString(fmt.Sprintf("msg_%d", index)) || !message.Role.IsString("assistant") || !message.Status.IsString("completed") || len(message.Children) != counts[index] {
			t.Fatal("message grouping or identity changed", message)
		}
		path := fmt.Sprintf("/response/output/%d", index)
		if message.Source == nil || message.Source.Path != path {
			t.Fatal("terminal message lost its real source position", message.Source)
		}
		hasPhase := false
		for _, metadata := range message.Metadata {
			if metadata.Name == "phase" && metadata.Location == "message" {
				hasPhase = true
				if metadata.Codec != Responses || metadata.Value != testValue(t, phases[index]) {
					t.Fatal("phase migrated to another message", index, metadata)
				}
			}
		}
		if !hasPhase {
			t.Fatal("message phase disappeared", index)
		}
		for part, child := range message.Children {
			kind := p.TextNode
			if isRefusal {
				kind = p.RefusalNode
			}
			if child.Kind != kind || !child.Payload.IsString(fmt.Sprintf("text_%d_%d", index, part)) {
				t.Fatal("ordered text or refusal content changed", child)
			}
			if child.Source == nil || child.Source.Path != fmt.Sprintf("%s/content/%d", path, part) {
				t.Fatal("terminal content lost its real source position", child.Source)
			}
		}
	}
}
