package builtin

import (
	"bytes"
	"errors"
	"maps"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageControlSourceStream(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	errors := responsesErrorEnvelopes()
	for _, fixture := range []struct {
		name   string
		frames []string
	}{
		{"keepalive", []string{`{"type":"keepalive"}`, progressStart, `{"type":"keepalive","sequence_number":1}`, progressEnd, `{"type":"keepalive","sequence_number":11}`}},
		{"nested-error", []string{errors[0]}},
		{"flat-error", []string{errors[1]}},
		{"failed-before-start", []string{`{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"type":"api_error","message":"failed"}}}`}},
		{"failed-string", []string{progressStart, `{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":"failed"}}`}},
		{"failure-ignores-output-and-usage", []string{progressStart, `{"type":"response.failed","sequence_number":1,"response":{"id":"r","status":"anything","output":42,"usage":false,"error":{"type":"server_error","message":"failed"}}}`}},
		{"failure-metadata", []string{`{"type":"error","sequence_number":1,"message":"failed","service_tier":"default","metadata":{"n":9007199254740993},"content_filters":null}`}},
		{"partial-tool-failure", []string{progressStart,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"function_call","id":"tool","call_id":"call","name":"lookup","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"tool","delta":"{\"n\":"}`,
			`{"type":"error","sequence_number":3,"message":"interrupted"}`}},
		{"usage-after-failure", []string{errors[0], `{"type":"response.in_progress","sequence_number":52,"response":{"usage":{"input_tokens":1}}}`}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			sourceTarget := candidate.Target(p.DecodeEvent, p.Scope{})
			sourceTarget.Direction = p.EncodeEvent
			replay, err := p.NewEventReplay(sourceTarget, candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			var oldEvents, newEvents []p.Event
			for index, raw := range fixture.frames {
				value := testValue(t, raw)
				before, err := legacy.DecodeFrame(t.Context(), value, oldOptions)
				if err != nil {
					t.Fatal("legacy control", index, err)
				}
				after, err := wire.DecodeFrame(t.Context(), value, newOptions)
				if err != nil {
					t.Fatal("declared control", index, err)
				}
				for _, event := range after.Events {
					if _, err := replay.Consume(event); err != nil {
						t.Fatal("declared semantic lifecycle", index, err)
					}
				}
				fields, err := value.ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case fields["type"].IsString("keepalive"):
					if len(after.Events) != 0 {
						t.Fatal("payload-free transport control became a semantic event", after.Events)
					}
				case fields["type"].IsString("response.output_item.added"):
					if len(after.Events) != 1 {
						t.Fatal("tool start split or disappeared", after.Events)
					}
					event := after.Events[0]
					if event.Type != p.ItemStarted || !event.ItemID.IsString("output:0") || event.Index == nil || *event.Index != 0 || event.Item == nil || event.Item.Kind != p.ToolCallNode || !event.Item.ID.IsString("tool") || !event.Item.CallID.IsString("call") || !event.Item.Name.IsString("lookup") || event.Item.Input == nil || event.Item.Input.Kind != p.JSONInput || !event.Item.Input.Value.IsZero() {
						t.Fatal("partial tool association changed", event)
					}
					if event.Item.Source == nil || event.Item.Source.Path != "/item" || event.Item.Source.Protocol != candidate.Identity() {
						t.Fatal("tool start lost its actual source", event.Item.Source)
					}
				default:
					assertResponsesControlObservations(t, candidate, before.Events, after.Events)
				}

				oldEvents, newEvents = append(oldEvents, before.Events...), append(newEvents, after.Events...)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			if err := replay.Finish(); err != nil {
				t.Fatal(err)
			}
			if fixture.name == "keepalive" {
				collector, err := p.NewResponseCollector(sourceTarget, candidate.ResourceLimits())
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range newEvents {
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal(err)
					}
				}
				response, err := collector.Finish()
				if err != nil || len(response.Content) != 0 || response.Usage != nil || !response.ID.IsString("r") || !response.Status.IsString("completed") {
					t.Fatal("transport controls changed the empty generation", response, err)
				}
			}
			if fixture.name == "nested-error" || fixture.name == "flat-error" {
				for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
					want := encodeMigratedStream(t, target, oldEvents, p.Scope{})
					got := encodeMigratedStream(t, target, streamEventsAtVersion(newEvents, p.SemanticSchemaVersion), p.Scope{})
					if !bytes.Equal(want.Bytes(), got.Bytes()) {
						t.Fatal("failure reconstruction changed", target, want.JSONString(), got.JSONString())
					}
				}
			}
		})
	}
}

func TestResponsesPackageControlTerminalBoundary(t *testing.T) {
	candidate := compileBuiltinPackage(t, "openai-responses")
	for _, fixture := range []struct{ frame, path string }{
		{`{"type":"error","sequence_number":11,"message":"too late"}`, "/type"},
		{`{"type":"response.failed","sequence_number":11,"response":{"id":"r","error":{"message":"too late"}}}`, "/type"},
		{`{"type":"keepalive","sequence_number":11,"vendor":42}`, "/type"},
		{`{"type":"keepalive","sequence_number":10}`, "/sequence_number"},
	} {
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{progressStart, progressEnd} {
			if err := wire.Consume(t.Context(), testValue(t, prefix)); err != nil {
				t.Fatal(err)
			}
		}
		err = wire.Consume(t.Context(), testValue(t, fixture.frame))
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) == 0 {
			t.Fatal("post-terminal frame was accepted", fixture.frame, err)
		}
		issue := failure.Issues[0]
		if issue.Code != p.UpstreamContractViolation || issue.Direction != p.DecodeEvent || issue.Protocol != candidate.Identity() || issue.Path != fixture.path {
			t.Fatal("terminal barrier lost its wire attribution", issue)
		}
	}
	validation, err := candidate.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), testValue(t, `{"type":"keepalive","sequence_number":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := validation.Finish(); err == nil {
		t.Fatal("heartbeat-only EOF invented a terminal")
	}
}

func TestResponsesPackageControlRejections(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	fixtures := append(responsesErrorRejections(), []struct{ body, path string }{
		{`{"type":"keepalive","sequence_number":null}`, "/sequence_number"},
		{`{"type":"keepalive","sequence_number":"1"}`, "/sequence_number"},
		{`{"type":"error","sequence_number":1,"error":{"type":"","message":"failed"}}`, "/error/type"},
		{`{"type":"response.failed","sequence_number":1,"response":{"error":null}}`, "/response/error"},
	}...)
	for _, fixture := range fixtures {
		wire, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		issue := requireResponsesWireRejection(t, compiled, wire.Consume(t.Context(), testValue(t, fixture.body)))
		if issue.Path != fixture.path {
			t.Fatal("invalid control lost its source field location", fixture.path, issue)
		}
	}
}

func requireResponsesWireRejection(t *testing.T, compiled *p.Compiled, err error) p.ConversionIssue {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) == 0 {
		t.Fatal("invalid source wire was accepted or lost its structured rejection", err)
	}
	issue := failure.Issues[0]
	if issue.Code == "" || issue.Severity != p.SeverityError || issue.Direction != p.DecodeEvent || issue.Protocol != compiled.Identity() || issue.Stage == "" || issue.Reason == "" || issue.Path == "" {
		t.Fatal("source rejection lost its attribution", issue)
	}
	return issue
}

func TestResponsesPackageControlPreservesExtensions(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"keepalive","sequence_number":1,"delta":"private"}`,
		`{"type":"keepalive","sequence_number":1,"service_tier":"default"}`,
		`{"type":"keepalive","sequence_number":1,"response":{"id":"r"}}`,
		`{"type":"error","sequence_number":1,"message":"failed","vendor":false}`,
	} {
		beforeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		afterOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		defer beforeOptions.State.Release()
		defer afterOptions.State.Release()
		value := testValue(t, raw)
		before, err := legacy.DecodeFrame(t.Context(), value, beforeOptions)
		if err != nil || len(before.Events) != 1 || before.Events[0].Unmapped == nil {
			t.Fatal("legacy fixture must preserve the unknown payload", before, err)
		}
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.Consume(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		after, err := candidate.DecodeFrame(t.Context(), value, afterOptions)
		if err != nil || len(after.Events) != 1 || after.Events[0].Type != before.Events[0].Type || after.Events[0].Unmapped == nil || len(after.Events[0].Metadata) != len(before.Events[0].Metadata) || after.Events[0].Error != before.Events[0].Error {
			t.Fatal("control lost or reclassified its payload", before, after, err)
		}
		expected, err := sonic.ConfigStd.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		unmapped := after.Events[0].Unmapped
		if unmapped.Source.Path != "" || unmapped.Source.Protocol != candidate.Identity() || unmapped.Source.Direction != p.DecodeEvent || !bytes.Equal(unmapped.Value.Bytes(), expected) {
			t.Fatal("control extension lost its original frame", unmapped)
		}
	}
}

func assertResponsesControlObservations(t *testing.T, compiled *p.Compiled, expected, actual []p.Event) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatal("control observation count changed", len(expected), len(actual))
	}
	assertValue := func(before, after any) {
		t.Helper()
		want, err := p.EncodeValue(before)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.EncodeValue(after)
		if err != nil || want != got {
			t.Fatal("observed control value changed", want.JSONString(), got.JSONString(), err)
		}
	}
	for index, event := range actual {
		before := expected[index]
		if event.SchemaVersion != compiled.CoreVersion() || event.Source != compiled.Identity() || event.Type != before.Type || event.ResponseID != before.ResponseID || event.ItemID != before.ItemID || event.CallID != before.CallID || event.ParentID != before.ParentID || event.Delta != before.Delta || event.Error != before.Error {
			t.Fatal("control identity, lifecycle or payload changed", event)
		}
		if event.Item != nil || event.Request != nil || event.Media != nil || event.Control != nil || event.ResponsesTurnState != nil {
			t.Fatal("control observation acquired unassociated generation or state payload", event)
		}
		assertValue(before.Usage, event.Usage)
		assertValue(before.Metadata, event.Metadata)
		if before.Response == nil {
			if event.Response != nil {
				t.Fatal("control invented a response snapshot", event.Response)
			}
			continue
		}
		response := event.Response
		if response == nil || response.SchemaVersion != compiled.CoreVersion() || response.Source != compiled.Identity() || response.ID != before.Response.ID || response.Model != before.Response.Model || response.Status != before.Response.Status || response.Error != before.Response.Error || len(response.Content) != 0 || len(before.Response.Content) != 0 {
			t.Fatal("empty response observation changed", response)
		}
		assertValue(before.Response.Usage, response.Usage)
		assertValue(before.Response.Metadata, response.Metadata)
		if !maps.Equal(before.Response.Attributes, response.Attributes) {
			t.Fatal("response attribute fields changed", before.Response.Attributes, response.Attributes)
		}
	}
}
