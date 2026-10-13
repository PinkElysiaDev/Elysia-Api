package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestUnknownEventFieldsRetainOriginalPaths(t *testing.T) {
	from := shippedProjectionProtocol(t, Chat)
	frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"id":"r","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","vendor_field":true},"finish_reason":null}],"vendor_root":{"private":"not in diagnostic"}}`), p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	to := shippedProjectionProtocol(t, Anthropic)
	issues := p.CheckEvent(frame.Events[0], p.Target{Protocol: to.Identity(), Direction: p.EncodeEvent, Capabilities: to.Definition().Capabilities}, p.DefaultLimits())
	wanted := map[string]bool{"/vendor_root": false, "/choices/0/delta/vendor_field": false}
	for _, issue := range issues {
		if _, ok := wanted[issue.Path]; ok {
			wanted[issue.Path] = true
		}
	}
	for path, found := range wanted {
		if !found {
			t.Fatal(path, issues)
		}
	}
}
