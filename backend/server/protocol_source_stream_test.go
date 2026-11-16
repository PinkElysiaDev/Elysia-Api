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

	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
)

func TestCollectorUsesDeclaredSourceIdentity(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	upstream := compileFixturePackage(t, presetPackage(t, p.PresetChatCompletionsID))
	var frames []p.Value
	for _, sample := range compiledPackage(upstream).Mapping.Samples {
		if sample.ID == "declared-stream-text-decode" {
			if err := sample.Input.Decode(&frames); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(frames) < 2 {
		t.Fatal("missing independent stream fixture")
	}
	var active atomic.Value
	active.Store(frames)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range active.Load().([]p.Value) {
			fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(upstream, upstream))
	if err != nil {
		t.Fatal(err)
	}
	var operation p.Operation
	var operationName string
	for name, candidate := range upstream.Operations() {
		if candidate.Kind == "generate" && candidate.Transport == p.SSE {
			operation, operationName = candidate, name
		}
	}
	candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, operationName: operationName,
		model:   config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL},
		binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.DeclaredCapabilities(), Transports: []p.Transport{p.SSE}}}
	request, err := upstream.DecodeRequest(t.Context(), []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{RequestID: "fixture-execution", StartedAt: time.Now(), bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
	result, err := s.collectProtocolGeneration(t.Context(), candidate, request, record, nil)
	if err != nil || result == nil {
		t.Fatalf("declared source collection: %v", err)
	}
	if len(record.pendingStreamEvents) == 0 || !strings.Contains(string(record.pendingStreamEvents[0]), `"id":"r"`) {
		t.Fatal("original frame missing from evidence")
	}
	changed := append([]p.Value(nil), frames...)
	changed[1], err = p.ApplyMutations(changed[1], []p.Mutation{{Op: p.SetValue, Path: "/id", Value: p.StringValue("different-response")}}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	active.Store(changed)
	if _, err := s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil); err == nil {
		t.Fatal("source identity change was accepted")
	}
	for _, ending := range []error{nil, context.Canceled, context.DeadlineExceeded} {
		t.Run(fmt.Sprint(ending), func(t *testing.T) {
			state := p.NewEvaluationState()
			defer state.Release()
			source, err := newProtocolSourceStream(t.Context(), candidate, candidate.conversionContext(candidate.compiled, true), p.EvaluationContext{State: state}, nil, func(*p.EventFrame, p.Object) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Push(frames[0], nil); err != nil {
				t.Fatal(err)
			}
			err = source.Finish(ending)
			if ending == nil && err == nil {
				t.Fatal("incomplete source lifecycle accepted EOF")
			}
			if ending != nil && !errors.Is(err, ending) {
				t.Fatal("source interruption was replaced", err)
			}
		})
	}
}

func TestSourceEventTargetKeepsDirectionalContract(t *testing.T) {
	definition := presetPackage(t, p.PresetChatCompletionsID)
	encoding := definition.Mapping.Directions[p.EncodeEvent]
	encoding.Capabilities = p.CapabilitySet{p.UsageCapability: true}
	definition.Mapping.Directions[p.EncodeEvent] = encoding
	compiled := compileFixturePackage(t, definition)
	capabilities := p.CapabilitySet{p.UsageCapability: true}
	candidate := gatewayCandidate{compiled: compiled, binding: p.Binding{Capabilities: capabilities}, scope: p.Scope{Account: "fixture"}}
	target := candidate.sourceEventTarget()
	if target.Direction != p.EncodeEvent || target.Protocol != compiled.Identity() || target.Scope != candidate.scope || target.Capabilities[p.TextCapability] {
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

func TestSourcePreludePreservesProviderEvidence(t *testing.T) {
	compiled := compileFixturePackage(t, presetPackage(t, p.PresetChatCompletionsID))
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	candidate := gatewayCandidate{compiled: compiled, conversion: conversion, operation: p.Operation{Transport: p.SSE}, binding: p.Binding{Capabilities: compiled.DeclaredCapabilities()}}
	sample := selectProtocolSamples(t, compiled, "declared-stream-text-decode")[0]
	for _, timestamp := range []int{1, 81} {
		t.Run(fmt.Sprint(timestamp), func(t *testing.T) {
			frames := chatStartupFrames(t, sample, timestamp)
			options, err := (p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: &p.DiagnosticSink{}}).WithOperation("source-prelude-fixture", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer options.State.Release()
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
			consumed := 0
			source, err := newProtocolSourceStream(t.Context(), candidate, candidate.conversionContext(compiled, true).WithOperation(options), options, record, func(frame *p.EventFrame, metadata p.Object) error {
				if consumed == 0 && !metadata["fixture"].IsString("original") {
					t.Fatal("pending transport metadata changed", metadata)
				}
				for _, event := range frame.Events {
					if !event.ResponseID.IsZero() && !event.ResponseID.IsString("r") {
						t.Fatal("provisional source identity reached semantic replay", event.ResponseID)
					}
				}
				consumed++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer source.release()
			metadata := p.Object{"fixture": p.StringValue("original")}
			if err := source.Push(frames[0], metadata); err != nil || consumed != 0 {
				t.Fatal("opening frame was not staged before source validation", consumed, err)
			}
			metadata["fixture"] = p.StringValue("reused")
			for _, frame := range frames[1:] {
				if err := source.Push(frame, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := source.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if consumed != len(frames) || len(record.pendingStreamEvents) != len(frames) || string(record.pendingStreamEvents[0]) != frames[0].JSONString() {
				t.Fatal("normalization changed or duplicated original provider evidence", consumed, len(record.pendingStreamEvents))
			}
			if len(options.Diagnostics.Issues()) == 0 {
				t.Fatal("source identity correction omitted its declared diagnostic")
			}
		})
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
	source, err := newProtocolSourceStream(t.Context(), candidate, candidate.conversionContext(candidate.compiled, true), p.EvaluationContext{State: p.NewEvaluationState()}, record, func(frame *p.EventFrame, _ p.Object) error {
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
			source, err := newProtocolSourceStream(t.Context(), candidate, candidate.conversionContext(candidate.compiled, true), p.EvaluationContext{}, nil, func(frame *p.EventFrame, _ p.Object) error {
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
