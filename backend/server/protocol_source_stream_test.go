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
	conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: 1, ID: "review", Rules: []p.ConversionRule{{ID: "chat-stream-identity", Enabled: true, Phase: p.ConversionSourceEvent, Action: "chat_stream_identity"}}})
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
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
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
		{SchemaVersion: 1, ID: "strict", Mode: "strict", Rules: conversion.Policy.Rules},
		{SchemaVersion: 1, ID: "disabled", Rules: []p.ConversionRule{{ID: "chat-stream-identity", Enabled: false, Phase: p.ConversionSourceEvent, Action: "chat_stream_identity"}}},
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
