package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAuditUnknownFailureRetainsCauseBeforeReplay(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, name := range []string{"***.failed", "vendor.failed", "unknown"} {
		fields, _ := liveAuditFixture(t, "unknown_failure").ReadObject()
		fields["type"] = p.StringValue(name)
		frame, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()})
		var failure *p.GenerationFailure
		var issue *p.ConversionError
		if frame != nil || !errors.As(err, &failure) || !errors.As(err, &issue) || !strings.Contains(err.Error(), "Synthetic account concurrency limit") {
			t.Fatal(frame, err)
		}
		if issue.Issues[0].Code != p.UpstreamContractViolation || issue.Issues[0].Path != "/type" || issue.Issues[0].Protocol.DefinitionID != p.PresetResponsesID || issue.Issues[0].Direction != p.DecodeEvent {
			t.Fatal(issue.Issues)
		}
	}
}

func TestAuditSourceIdentityIsCheckedBeforeProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Chat)
	var frames []p.Value
	_ = liveAuditFixture(t, "changed_chat_identity").Decode(&frames)
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
	for i, frame := range frames {
		_, err := from.DecodeFrame(t.Context(), frame, opts)
		if i < 2 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var issue *p.ConversionError
		if !errors.As(err, &issue) || issue.Issues[0].Code != p.UpstreamContractViolation || issue.Issues[0].Path != "/id" || issue.Issues[0].Direction != p.DecodeEvent || !strings.Contains(err.Error(), "frame 3") {
			t.Fatal(err)
		}
	}
}
