package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestRequestReasoningDiagnosticDirection(t *testing.T) {
	for _, fixture := range []struct{ codec, body, path string }{
		{Anthropic, `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi"}]}`, "/thinking/budget_tokens"},
		{Gemini, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","thinkingBudget":1024}}}`, "/generationConfig/thinkingConfig"},
	} {
		t.Run(fixture.codec, func(t *testing.T) {
			compiled := shippedProjectionProtocol(t, fixture.codec)
			options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})
			_, err := compiled.DecodeRequest(t.Context(), []byte(fixture.body), options)
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal("missing request diagnostic", err)
			}
			issue := failure.Issues[0]
			if issue.Code != p.InvalidInput || issue.Path != fixture.path || issue.Stage != "wire" || issue.Direction != p.DecodeRequest || issue.Protocol != compiled.Identity() || strings.Contains(issue.Suggestion, "upstream stream") {
				t.Fatal("request check was attributed to a stream", issue)
			}
			// Both entry points execute the source Spec before the Mapping.
			_, issues := compiled.Execute(t.Context(), p.DecodeRequest, testValue(t, fixture.body), options)
			if len(issues) != 1 || issues[0].Direction != p.DecodeRequest || issues[0].Stage != "wire" || issues[0].Code != issue.Code || issues[0].Path != issue.Path || issues[0].Reason != issue.Reason {
				t.Fatal("raw validation invented attribution or changed the rejection", issues)
			}
		})
	}
	compiled := shippedProjectionProtocol(t, Gemini)
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{},
		Reasoning:  &p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue("high"), EffortScope: "reasoning"},
		Parameters: p.Object{"wire:gemini": testValue(t, `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"low"}}}`)}}
	_, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Direction != p.EncodeRequest || failure.Issues[0].Stage == "stream" || failure.Issues[0].Code != p.InvalidInput || errors.Unwrap(err) == nil || !strings.Contains(err.Error(), `conflicting merge field "thinkingLevel"`) {
		t.Fatal("rendering check lost its request direction or cause", err)
	}
}
