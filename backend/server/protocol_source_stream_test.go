package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestCollectorAppliesStartupConversion(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), builtin.Modules(), nil)
	defs, _ := builtin.Definitions()
	var upstream *p.Compiled
	for _, raw := range defs {
		c, issues := compiler.Compile(raw.Bytes())
		if c == nil {
			t.Fatal(issues)
		}
		if c.Identity().DefinitionID == p.PresetChatCompletionsID {
			upstream = c
		}
	}
	var sample p.Sample
	for _, v := range upstream.Definition().Samples {
		if v.ID == "stream-text-decode" {
			sample = v
		}
	}
	frames := chatStartupFrames(t, sample, 1)
	var active atomic.Value
	active.Store(frames)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range active.Load().([]p.Value) {
			raw, _ := sonic.ConfigStd.Marshal(f)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "review", Rules: []p.ConversionRule{{ID: "chat-stream-identity", Enabled: true, Phase: p.ConversionSourceEvent, Action: "chat_stream_identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	var operation p.Operation
	var operationName string
	for name, op := range upstream.Operations() {
		if op.Kind == "generate" && op.Transport == p.SSE {
			operation, operationName = op, name
		}
	}
	candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, operationName: operationName, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []p.Transport{p.SSE}}}
	request, err := upstream.DecodeRequest(t.Context(), []byte("{\"model\":\"m\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{RequestID: "fixture-execution", StartedAt: time.Now(), bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
	result, err := s.collectProtocolGeneration(t.Context(), candidate, request, record, nil)
	if err != nil || result == nil {
		t.Fatalf("enabled startup rule failed: %v", err)
	}
	if len(record.ConversionIssues) != 1 {
		t.Fatalf("collector did not retain normalization diagnostics: %+v", record.ConversionIssues)
	}
	issue := record.ConversionIssues[0]
	if issue.RuleID != "chat-stream-identity" || issue.PolicyHash == "" || issue.Path != "/id" || issue.Direction != p.DecodeEvent || !strings.Contains(issue.Reason, "frame 1") {
		t.Fatalf("normalization provenance lost: %+v", issue)
	}
	if len(record.pendingStreamEvents) == 0 || !strings.Contains(string(record.pendingStreamEvents[0]), "synthetic-prelude") {
		t.Fatal("original provider frame was replaced in call evidence")
	}
	candidate.conversion = nil
	_, err = s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("bare codec probe must reject invalid identity: %v", err)
	}
	candidate.conversion = conversion
	for _, policy := range []p.ConversionPolicy{
		{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "strict", Mode: "strict", Rules: conversion.Policy.Rules},
		{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "disabled", Rules: []p.ConversionRule{{ID: "chat-stream-identity", Enabled: false, Phase: p.ConversionSourceEvent, Action: "chat_stream_identity"}}},
	} {
		candidate.conversion, err = p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil); err == nil {
			t.Fatalf("%s accepted identity correction", policy.ID)
		}
	}
	candidate.conversion = conversion

	route := p.ConversionContext{Source: upstream.Identity(), Target: upstream.Identity(), Transport: p.SSE}
	state := p.NewSourceEventState(conversion, route, upstream, upstream, &p.DiagnosticSink{})
	sequence, _ := p.EncodeValue(frames)
	normalized, err := state.Sequence(t.Context(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	var canonical []p.Value
	if err := normalized.Decode(&canonical); err != nil {
		t.Fatal(err)
	}
	active.Store(canonical)
	result, err = s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("missing result")
	}
	t.Log("same collector after existing source normalization succeeds")

	for _, ending := range []error{nil, context.Canceled, context.DeadlineExceeded} {
		t.Run(fmt.Sprint(ending), func(t *testing.T) {
			consumed := 0
			source, err := newProtocolSourceStream(t.Context(), candidate, upstream, route, p.EvaluationContext{State: p.NewEvaluationState()}, nil, func(frame *p.EventFrame, _ p.Object) error {
				consumed += len(frame.Events)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Push(frames[0], nil); err != nil {
				t.Fatal(err)
			}
			if consumed != 0 {
				t.Fatal("buffered prelude was delivered early")
			}
			err = source.Finish(ending)
			if ending == nil {
				if err == nil || consumed == 0 {
					t.Fatal("clean EOF must release the prelude and reject the incomplete lifecycle")
				}
			} else if !errors.Is(err, ending) || consumed != 0 {
				t.Fatalf("interrupted prelude generated events or lost cause: %d %v", consumed, err)
			}
		})
	}
}

func TestSourceEventTargetKeepsDirectionalContract(t *testing.T) {
	definition := presetDefinition(t, p.PresetChatCompletionsID)
	definition.Directions[p.EncodeEvent] = p.Mapping{Module: builtin.Anthropic}
	compiled := compileFixtureDefinition(t, definition)
	capabilities := p.CapabilitySet{p.UsageCapability: true}
	candidate := gatewayCandidate{compiled: compiled, binding: p.Binding{Capabilities: capabilities}, scope: p.Scope{Account: "fixture"}}
	target := candidate.sourceEventTarget()
	if target.Codec != builtin.Chat || target.Direction != p.EncodeEvent || target.Protocol != compiled.Identity() || target.Scope != candidate.scope || target.Capabilities[p.TextCapability] {
		t.Fatal("source target used the encoder or widened the binding", target)
	}
	event := p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}
	if issues := p.CheckEvent(event, target, compiled.ResourceLimits()); len(issues) == 0 || issues[0].Code != p.UnsupportedCapability || len(capabilities) != 1 {
		t.Fatal("binding capability checks changed or mutated their borrowed map", issues, capabilities)
	}
	candidate.binding.Capabilities = nil
	if candidate.sourceEventTarget().Capabilities != nil {
		t.Fatal("nil binding acquired default capabilities")
	}
}

func TestPackageSourceStreamCollectsUsage(t *testing.T) {
	compiled := compileSourceStreamPackage(t, sourceStreamPackage(t))
	candidate := gatewayCandidate{compiled: compiled, binding: p.Binding{Capabilities: compiled.Capabilities(p.DecodeEvent)}}
	collector, err := p.NewResponseCollector(candidate.sourceEventTarget(), compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
	consumed := 0
	source, err := newProtocolSourceStream(t.Context(), candidate, compiled, p.ConversionContext{}, p.EvaluationContext{State: p.NewEvaluationState()}, record, func(frame *p.EventFrame, _ p.Object) error {
		for _, event := range frame.Events {
			if !event.Sequence.IsZero() || event.SchemaVersion != p.ProtocolCoreVersion {
				t.Fatal("fixture mapping or event version changed", event)
			}
			if _, _, err := collector.Consume(event); err != nil {
				return err
			}
			consumed++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := []string{
		`{"type":"response.started","responseId":"r","sequence":0}`,
		`{"type":"item.started","responseId":"r","sequence":1,"itemId":"text","item":{"kind":"text"}}`,
		`{"type":"item.delta","responseId":"r","sequence":2,"itemId":"text","delta":"hi"}`,
		`{"type":"usage.updated","responseId":"r","sequence":3,"usage":{"input":{"count":7,"origin":"observed"}}}`,
		`{"type":"item.finished","responseId":"r","sequence":4,"itemId":"text"}`,
		`{"type":"response.finished","responseId":"r","sequence":5}`,
		`{"type":"usage.updated","responseId":"r","sequence":6,"usage":{"input":{"count":7,"origin":"observed"},"output":{"count":2,"origin":"observed"},"total":{"count":9,"origin":"observed"}}}`,
	}
	for _, raw := range frames {
		if err := source.Push(mustProtocolValue(t, raw), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Finish(nil); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil || response.SchemaVersion != p.ProtocolCoreVersion || len(response.Content) != 1 || !response.Content[0].Payload.IsString("hi") || response.Usage.Total.Count != 9 {
		t.Fatal("source collection lost text, core version or usage", response, err)
	}
	if consumed != len(frames) || record.ProtocolUsage.Input.Count != 7 || record.ProtocolUsage.Output.Count != 2 || record.ProtocolUsage.Total.Count != 9 || len(record.pendingStreamEvents) != len(frames) {
		t.Fatal("source delivery or accounting duplicated a frame", consumed, record.ProtocolUsage)
	}
}

func TestPackageSourceStreamChecksOriginalLifecycle(t *testing.T) {
	readFailure := errors.New("synthetic source read failure")
	for _, fixture := range []struct {
		name      string
		fakeEnd   bool
		readError error
		path      string
	}{
		{"hidden-sequence", false, nil, "/sequence"},
		{"hidden-eof", true, nil, "/type"},
		{"cancel-before-eof", true, context.Canceled, ""},
		{"read-error-before-eof", true, readFailure, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := sourceStreamPackage(t)
			if fixture.fakeEnd {
				document.Mapping.Directions[p.DecodeEvent].Program.Fields["type"] = p.Expression{Op: "literal", Value: p.StringValue(string(p.ResponseFinished))}
			}
			compiled := compileSourceStreamPackage(t, document)
			candidate := gatewayCandidate{compiled: compiled, binding: p.Binding{Capabilities: document.Mapping.Capabilities}}
			consumed := 0
			source, err := newProtocolSourceStream(t.Context(), candidate, compiled, p.ConversionContext{}, p.EvaluationContext{}, nil, func(frame *p.EventFrame, _ p.Object) error {
				consumed += len(frame.Events)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Push(mustProtocolValue(t, `{"type":"response.started","responseId":"r","sequence":0}`), nil); err != nil {
				t.Fatal(err)
			}
			if !fixture.fakeEnd {
				err = source.Push(mustProtocolValue(t, `{"type":"response.finished","responseId":"r","sequence":0}`), nil)
				if err == nil {
					t.Fatal("mapping hid the repeated provider sequence")
				}
				original := err
				err = source.Finish(err)
				if !errors.Is(err, original) {
					t.Fatal("completion replaced the frame failure", err)
				}
			} else {
				err = source.Finish(fixture.readError)
			}
			if fixture.readError != nil {
				if !errors.Is(err, fixture.readError) {
					t.Fatal("missing terminal replaced the read/cancellation cause", err)
				}
			} else {
				var failure *p.ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.UpstreamContractViolation || failure.Issues[0].Direction != p.DecodeEvent || failure.Issues[0].Path != fixture.path {
					t.Fatal("original wire lifecycle was not enforced", err)
				}
			}
			if consumed != 1 {
				t.Fatal("invalid frame or synthetic terminal reached the consumer twice", consumed)
			}
		})
	}
}
