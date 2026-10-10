package builtin

import (
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
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
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
				if policy.Rules[i].Action == "response_metadata" {
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
			projected, err := c.Event(t.Context(), delta, p.ConversionContext{}, sink)
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
				probe := p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: p.MetadataUpdated, Metadata: projected.Metadata}
				if _, err := to.EncodeFrames(t.Context(), probe, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil || !strings.Contains(err.Error(), "/obfuscation") {
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
	for _, value := range []string{`null`, `42`, `{}`, `[]`} {
		if err := p.ValidateMetadataValue(Responses, "event:response.output_text.delta", "obfuscation", testValue(t, value), "/obfuscation"); err == nil {
			t.Fatal(value)
		}
	}
	if p.MetadataFieldType(Responses, "event:response.completed", "obfuscation") != "" || p.MetadataFieldType(Responses, "response", "obfuscation") != "" {
		t.Fatal("padding recognized outside declared delta locations")
	}
}
