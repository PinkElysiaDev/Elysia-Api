package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func responsesPaddedTextFrames() []string {
	return []string{
		`{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"status":"in_progress","output":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"O","obfuscation":"pad-one"}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"K","obfuscation":"pad-two"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"OK","annotations":[]}}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","created_at":1,"status":"completed","output":[{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`,
	}
}

func TestResponsesPaddingRetainsEventOwnershipAndProjects(t *testing.T) {
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		frames := auditConvertFrames(t, Responses, target, responsesPaddedTextFrames())
		seen := 0
		for _, frame := range frames {
			fields, _ := frame.ReadObject()
			if fields["obfuscation"].IsZero() {
				continue
			}
			seen++
			if target != Responses || fields["type"] != p.StringValue("response.output_text.delta") {
				t.Fatal(string(frame.Bytes()))
			}
			if fields["delta"] == p.StringValue("O") && fields["obfuscation"] != p.StringValue("pad-one") || fields["delta"] == p.StringValue("K") && fields["obfuscation"] != p.StringValue("pad-two") {
				t.Fatal("padding moved between deltas")
			}
		}
		if target == Responses && seen != 2 {
			t.Fatalf("padding count=%d", seen)
		}
	}
}

func TestResponsesPaddingStrictDisabledAndInvalid(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	opts := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	var delta p.Event
	for _, body := range responsesPaddedTextFrames()[:4] {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range frame.Events {
			if len(e.Metadata) > 0 {
				delta = e
			}
		}
	}
	if len(delta.Metadata) != 1 || delta.Metadata[0].Name != "obfuscation" {
		t.Fatal(delta)
	}
	for _, mode := range []string{"compatible", "strict"} {
		for _, enabled := range []bool{false, true} {
			policy := p.DefaultConversionPolicy(to, from)
			policy.Mode = mode
			for i := range policy.Rules {
				if policy.Rules[i].ID == "response-metadata" || policy.Rules[i].ID == "event-metadata" || policy.Rules[i].ID == "history-response-metadata" {
					policy.Rules[i].Enabled = enabled
				} else {
					policy.Rules[i].Enabled = false
				}
			}
			c, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := c.Event(t.Context(), delta, fixtureConversionContext(t, c, p.ConversionEvent, delta.Source), sink)
			if enabled && mode == "strict" {
				if err == nil || !strings.Contains(err.Error(), "/obfuscation") {
					t.Fatal(err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !enabled {
				if len(projected.Metadata) != 1 {
					t.Fatal("disabled rule omitted padding")
				}
				probe := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.MetadataUpdated, Metadata: projected.Metadata}
				if _, err := to.EncodeFrames(t.Context(), probe, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})); err == nil || !strings.Contains(err.Error(), "/obfuscation") {
					t.Fatal("disabled projection still encoded", err)
				}
				continue
			}
			if len(projected.Metadata) != 0 {
				t.Fatal(projected.Metadata)
			}
			found := false
			for _, i := range sink.Issues() {
				if i.Path == "/obfuscation" {
					found = true
					if i.RuleID == "" || i.PolicyHash != c.Hash || i.Fidelity != "lossy_compatible" {
						t.Fatal(i)
					}
				}
			}
			if !found {
				t.Fatal("padding projection lacks diagnostic")
			}
		}
	}
}

func TestResponsesPaddingContractRejectsInvalidTypes(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	for _, kind := range []string{
		"response.output_text.delta", "response.refusal.delta", "response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
	} {
		for _, value := range []string{`null`, `42`, `{}`, `[]`} {
			t.Run(kind+"/"+value, func(t *testing.T) {
				frame := testValue(t, fmt.Sprintf(`{"type":%q,"obfuscation":%s}`, kind, value))
				validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
				if err != nil {
					t.Fatal(err)
				}
				defer validation.Release()
				for direction, err := range map[p.Direction]error{
					p.DecodeEvent: validation.Consume(t.Context(), frame),
					p.EncodeEvent: compiled.ValidateWireOutput(p.EncodeEvent, frame),
				} {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != "/obfuscation" || failure.Issues[0].Direction != direction || failure.Issues[0].Protocol != compiled.Identity() {
						t.Fatal("padding contract lost its boundary or field location", direction, err)
					}
				}
			})
		}
	}
}

func TestResponsesPaddingOutsideDeltaRetainsExtension(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	frames := responsesPaddedTextFrames()
	for index, raw := range frames {
		if index == len(frames)-1 {
			raw = strings.Replace(raw, `{`, `{"obfuscation":"unknown-location",`, 1)
		}
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		if index == len(frames)-1 {
			hasUnmapped := false
			for _, event := range frame.Events {
				for _, metadata := range event.Metadata {
					if metadata.Name == "obfuscation" {
						t.Fatal("terminal extension was classified as padding")
					}
				}
				hasUnmapped = hasUnmapped || event.Unmapped != nil
			}
			if !hasUnmapped {
				t.Fatal("unknown terminal extension disappeared")
			}
		}
	}
	raw := strings.Replace(auditResponses[Responses], `{`, `{"obfuscation":"unknown-location",`, 1)
	response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range response.Metadata {
		if metadata.Name == "obfuscation" {
			t.Fatal("response extension was classified as event padding")
		}
	}
	wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil || !strings.Contains(string(wire), `"obfuscation":"unknown-location"`) {
		t.Fatal("same-wire response extension was lost", string(wire), err)
	}
}
