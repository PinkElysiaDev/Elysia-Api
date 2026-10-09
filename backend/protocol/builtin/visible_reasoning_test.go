package builtin

import (
	"strings"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

func TestVisibleReasoningBindingEvidence(t *testing.T) {
	ingress, upstream := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	d := upstream.Definition()
	d.Samples = append(d.Samples, p.Sample{ID: "visible-reasoning-response", Direction: p.DecodeResponse,
		Input:    testValue(t, `{"content":[{"type":"thinking","thinking":"full thought"},{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"status":"completed","content":[{"kind":"message","role":"assistant","children":[{"kind":"reasoning","payload":"full thought"},{"kind":"text","payload":"OK"}]}],"attributes":{"finishReason":"stop"}}`),
	})
	raw, _ := p.EncodeValue(d)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	upstream, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), upstream); !report.Passed {
		t.Fatal(report.Issues)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(ingress, upstream))
	report := p.VerifyBindingCombination(t.Context(), ingress, upstream, p.CapabilitySet{p.TextCapability: true, p.ReasoningCapability: true}, c)
	// This witness proves visible output only. The complete reasoning binding
	// still rejects native Responses summary history for an Anthropic target.
	for _, check := range report.Checks {
		if check.SampleID == "visible-reasoning-response" {
			if !check.Passed || check.Rejected {
				t.Fatal(check)
			}
			return
		}
	}
	t.Fatal("reasoning regression was not verified")
}

func TestVisibleReasoningContinuationDigestSurvivesResponses(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Responses)
	scope := p.Scope{Provider: "provider", Account: "account", Model: "m"}
	r, err := from.DecodeResponse(t.Context(), []byte(`{"content":[{"type":"thinking","thinking":"signed thought","signature":"synthetic-signature"}]}`), p.EvaluationContext{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	node := r.Content[0].Children[0]
	codec, _ := p.NewContinuationCodec([]byte("test-only-secret"))
	record := p.ContinuationRecord{Version: 1, Owner: "owner", Scope: scope, Protocol: from.Identity(), Node: node, Digest: p.ContinuationNodeDigest(node), ExpiresAt: time.Now().Unix() + 60}
	sealed, err := codec.Seal(record, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// The carrier contains the signature; the visible projection contains text.
	node.Resources = nil
	request := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: []p.Node{node}}
	wire, err := to.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	back, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := codec.Open(sealed, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RestoreContinuation(&back.Content[0], opened, "owner", scope, from.Identity()); err != nil || !p.HasSignature(back.Content[0]) {
		t.Fatal(err)
	}
	back.Content[0].Payload = p.StringValue("edited")
	if err := p.RestoreContinuation(&back.Content[0], opened, "owner", scope, from.Identity()); err == nil {
		t.Fatal("tampered visible thought restored")
	}
}

func TestVisibleThinkingToResponsesJSON(t *testing.T) {
	inputs := map[string]string{
		Chat:      `{"id":"r","model":"m","choices":[{"message":{"role":"assistant","reasoning_content":"think & verify","content":"OK"},"finish_reason":"stop"}]}`,
		Anthropic: `{"id":"r","model":"m","role":"assistant","content":[{"type":"thinking","thinking":"think & verify"},{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`,
		Gemini:    `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"think & verify"},{"text":"OK"}]},"finishReason":"STOP"}]}`,
	}
	for source, raw := range inputs {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(source+"/"+mode, func(t *testing.T) {
				from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, Responses)
				policy := p.DefaultConversionPolicy(to, from)
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				r, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				r, err = conversion.Response(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
					t.Fatal(err)
				}
				decoded, err := to.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				if len(decoded.Content) != 2 || decoded.Content[0].Kind != p.ReasoningNode || decoded.Content[0].ReasoningForm != "" || decoded.Content[0].Payload != p.StringValue("think & verify") || len(decoded.Content[0].Children) != 0 {
					t.Fatal("visible thinking changed representation", decoded.Content)
				}
				if strings.Contains(string(wire), `"type":"summary_text"`) {
					t.Fatal("visible thinking became summary")
				}
			})
		}
	}
}

func TestVisibleThinkingResponsesStreamAndHistory(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Responses)
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Delivery: &p.DeliveryState{ID: p.StringValue("r"), Created: testValue(t, "1")}, Model: "m"}
	index := 0
	events := []p.Event{
		{Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m")}},
		{Type: p.ItemStarted, Index: &index, ItemID: p.StringValue("thought"), Item: &p.Node{Kind: p.ReasoningNode}},
		{Type: p.ItemDelta, Index: &index, ItemID: p.StringValue("thought"), Delta: p.StringValue("first ")},
		{Type: p.ItemDelta, Index: &index, ItemID: p.StringValue("thought"), Delta: p.StringValue("second")},
		{Type: p.ItemFinished, Index: &index, ItemID: p.StringValue("thought"), Item: &p.Node{Kind: p.ReasoningNode, Payload: p.StringValue("first second")}},
		{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed")}},
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	var frames []p.Value
	for _, event := range events {
		event.SchemaVersion, event.Source = 1, from.Identity()
		out, err := conversion.Event(t.Context(), event, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := to.EncodeFrames(t.Context(), out, options)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, batch...)
	}
	tail, err := to.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	frames = append(frames, tail...)
	collector, err := p.NewResponseCollector(p.Target{Protocol: to.Identity(), Direction: p.EncodeEvent, Capabilities: to.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	decodeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
	var kinds []string
	for _, frame := range frames {
		if err := to.ValidateWireOutput(p.EncodeEvent, frame); err != nil {
			t.Fatal(err, string(frame.Bytes()))
		}
		fields, _ := frame.ReadObject()
		kind, _ := stringValue(fields["type"])
		kinds = append(kinds, kind)
		decoded, err := to.DecodeFrame(t.Context(), frame, decodeOptions)
		if err != nil {
			t.Fatal(err, kind)
		}
		for _, event := range decoded.Events {
			if event.Unmapped != nil {
				t.Fatal("reasoning event remained opaque", kind)
			}
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err, kind)
			}
		}
	}
	expected := "response.created,response.output_item.added,response.content_part.added,response.reasoning_text.delta,response.reasoning_text.delta,response.reasoning_text.done,response.content_part.done,response.output_item.done,response.completed"
	if strings.Join(kinds, ",") != expected {
		t.Fatal(kinds)
	}
	r, err := collector.Finish()
	if err != nil || len(r.Content) != 1 || r.Content[0].Payload != p.StringValue("first second") || r.Content[0].ReasoningForm != "" {
		t.Fatal(r, err)
	}
	// A returned reasoning item is valid input history and stays visible.
	request := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: r.Content}
	wire, err := to.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
	if err != nil || len(decoded.Content) != 1 || decoded.Content[0].Payload != p.StringValue("first second") {
		t.Fatal(decoded, err)
	}
}

func TestVisibleReasoningComplexNativeStructureNotDiscarded(t *testing.T) {
	to := shippedProjectionProtocol(t, Responses)
	for _, item := range []string{
		`{"type":"reasoning","id":"rs","summary":[],"content":[{"type":"reasoning_text","text":"first"},{"type":"reasoning_text","text":"second"}]}`,
		`{"type":"reasoning","id":"rs","summary":[{"type":"summary_text","text":"brief"}],"content":[{"type":"reasoning_text","text":"full"}]}`,
		`{"type":"reasoning","id":"rs","summary":[],"content":[{"type":"reasoning_text","text":"full","vendor":9007199254740993}]}`,
	} {
		raw := `{"model":"m","input":[` + item + `]}`
		r, err := to.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil // Force reconstruction instead of accepting native bytes.
		wire, err := to.EncodeRequest(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := testValue(t, string(wire)).ReadObject()
		input, _ := readArray(fields["input"])
		sameJSON(t, input[0].Bytes(), item)
		foreign := shippedProjectionProtocol(t, Anthropic)
		if _, err := foreign.EncodeRequest(t.Context(), r, p.EvaluationContext{}); err == nil {
			t.Fatal("complex reasoning lost its structure")
		}
	}
}

func TestVisibleReasoningRejectsMalformedStream(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"orphan"}`,
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"orphan"}`,
	} {
		c := shippedProjectionProtocol(t, Responses)
		if _, err := c.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
			t.Fatal("orphan reasoning must not masquerade as an opaque native event")
		}
	}
	for _, bad := range []string{
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":1,"delta":"wrong association"}`,
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"replacement"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":"prefix"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs","summary":[],"content":[{"type":"reasoning_text","text":"prefix"}]}}`,
	} {
		c := shippedProjectionProtocol(t, Responses)
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		for _, raw := range []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs","summary":[],"content":[]}}`,
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`,
			`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"prefix"}`,
		} {
			if _, err := c.DecodeFrame(t.Context(), testValue(t, raw), options); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.DecodeFrame(t.Context(), testValue(t, bad), options); err == nil {
			t.Fatal("invalid stream accepted", bad)
		}
	}
}
