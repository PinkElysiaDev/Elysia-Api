package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

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

func TestResponsesPackageControlSourceStream(t *testing.T) {
	candidate := testCompiled(t, Responses)
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
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			defer wire.Release()
			target := candidate.Target(p.DecodeEvent, p.Scope{})
			target.Direction = p.EncodeEvent
			replay, err := p.NewEventReplay(target, candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer replay.Release()
			var observed []p.Event
			failures := 0
			for index, raw := range fixture.frames {
				frame, err := wire.DecodeFrame(t.Context(), testValue(t, raw), options)
				if fixture.name == "usage-after-failure" && index == 1 {
					issue := requireResponsesWireRejection(t, candidate, err)
					if issue.Path != "/type" {
						t.Fatal("post-failure generation crossed its terminal", issue)
					}
					break
				}
				if err != nil {
					t.Fatal(index, err)
				}
				fields, _ := testValue(t, raw).ReadObject()
				if fields["type"].IsString("keepalive") && len(frame.Events) != 0 {
					t.Fatal("transport-only keepalive became generation")
				}
				for _, event := range frame.Events {
					if _, err := replay.Consume(event); err != nil {
						t.Fatal(index, err)
					}
					if event.Type == p.OperationFailed {
						failures++
						failure, err := event.Error.ReadObject()
						if err != nil || failure["message"].IsZero() || event.Usage != nil || event.Item != nil {
							t.Fatal("failure became generated content or usage", event, err)
						}
						message := "failed"
						if fixture.name == "nested-error" || fixture.name == "flat-error" || fixture.name == "usage-after-failure" {
							message = "provider failed"
						}
						if fixture.name == "partial-tool-failure" {
							message = "interrupted"
						}
						if !failure["message"].IsString(message) {
							t.Fatal("failure message changed", event.Error)
						}
					}
					if fields["type"].IsString("response.output_item.added") {
						if event.Type != p.ItemStarted || !event.ItemID.IsString("output:0") || event.Item == nil || !event.Item.CallID.IsString("call") || !event.Item.Name.IsString("lookup") || event.Item.Source == nil || event.Item.Source.Path != "/item" {
							t.Fatal("partial tool lost identity or source attribution", event)
						}
					}
					observed = append(observed, event)
				}
			}
			if err := wire.Finish(); fixture.name == "usage-after-failure" {
				if err == nil {
					t.Fatal("rejected source stream finished successfully")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if err := replay.Finish(); err != nil {
				t.Fatal(err)
			}
			if fixture.name != "keepalive" {
				if failures != 1 {
					t.Fatal("failure terminal disappeared or duplicated", failures)
				}
				if fixture.name == "failure-metadata" {
					encoded, _ := p.EncodeValue(observed)
					if !strings.Contains(encoded.JSONString(), "9007199254740993") || !strings.Contains(encoded.JSONString(), "content_filters") {
						t.Fatal("failure lost its independent metadata", encoded)
					}
				}
				return
			}
			collector, err := p.NewResponseCollector(target, candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer collector.Release()
			for _, event := range observed {
				if _, _, err := collector.Consume(event); err != nil {
					t.Fatal(err)
				}
			}
			response, err := collector.Finish()
			if err != nil || len(response.Content) != 0 || response.Usage != nil || !response.ID.IsString("r") || !response.Status.IsString("completed") {
				t.Fatal("keepalive changed the empty generation", response, err)
			}
		})
	}
}
