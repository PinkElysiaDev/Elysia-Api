package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageVisibleReasoningProjection(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	var events []p.Event
	if err := testValue(t, `[
		{"schemaVersion":2,"type":"response.started","response":{"schemaVersion":2,"id":"r","model":"m","usage":{"input":{"count":1,"origin":"observed"},"output":{"count":0,"origin":"observed"}}}},
		{"schemaVersion":2,"type":"item.started","itemId":"thought","item":{"kind":"reasoning","reasoningForm":"structured"}},
		{"schemaVersion":2,"type":"item.started","itemId":"visible","parentId":"thought","collection":"reasoning_content","item":{"kind":"text","payload":""}},
		{"schemaVersion":2,"type":"item.delta","itemId":"visible","parentId":"thought","collection":"reasoning_content","delta":"visible "},
		{"schemaVersion":2,"type":"item.snapshot","itemId":"visible","parentId":"thought","collection":"reasoning_content","item":{"kind":"text","payload":"visible thought"}},
		{"schemaVersion":2,"type":"item.finished","itemId":"visible","parentId":"thought","collection":"reasoning_content","item":{"kind":"text","payload":"visible thought"}},
		{"schemaVersion":2,"type":"item.finished","itemId":"thought","item":{"kind":"reasoning","reasoningForm":"structured"}},
		{"schemaVersion":2,"type":"item.started","itemId":"answer","item":{"kind":"text","payload":"OK"}},
		{"schemaVersion":2,"type":"item.finished","itemId":"answer"},
		{"schemaVersion":2,"type":"response.finished","response":{"schemaVersion":2,"id":"r","model":"m","status":"completed","usage":{"input":{"count":1,"origin":"observed"},"output":{"count":2,"origin":"observed"}}}}
	]`).Decode(&events); err != nil {
		t.Fatal(err)
	}
	output := encodeDeclaredTargetStream(t, compiled, events, p.Sample{}, true)
	var frames []p.Value
	if err := output.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	wire := make([]string, len(frames))
	for i, frame := range frames {
		wire[i] = frame.JSONString()
	}
	_, response := decodePackageStream(t, compiled, wire, p.Scope{})
	leaves := messageLeaves(t, response)
	if len(leaves) != 2 || leaves[0].Kind != p.ReasoningNode || !leaves[0].Payload.IsString("visible thought") || !leaves[1].Payload.IsString("OK") {
		t.Fatal("reasoning parts changed visible content or block order", response)
	}
	original := p.Node{Kind: p.ReasoningNode, ReasoningForm: p.StructuredReasoning, ReasoningContent: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("visible thought")}}}
	if p.ContinuationNodeDigest(original) != p.ContinuationNodeDigest(leaves[0]) {
		t.Fatal("visible projection changed authenticated continuation meaning")
	}
	if strings.Count(output.JSONString(), `"type":"thinking"`) != 1 || !strings.Contains(output.JSONString(), `"index":1`) {
		t.Fatal("collection events allocated extra output blocks", output.JSONString())
	}

	for _, collection := range []string{"summary", "reasoning_content"} {
		t.Run(collection, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			defer options.State.Release()
			for _, event := range events[:6] {
				if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
					t.Fatal(err)
				}
			}
			additional := events[2]
			additional.ItemID, additional.Collection = p.StringValue("second"), collection
			if _, err := compiled.EncodeFrames(t.Context(), additional, options); err == nil || !strings.Contains(err.Error(), "unsupported_capability") {
				t.Fatal("target silently merged a distinct reasoning part", err)
			}
		})
	}
}
