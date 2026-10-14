package server

import (
	"encoding/json"
	"fmt"
	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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
			raw, _ := json.Marshal(f)
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
	for _, op := range upstream.Operations() {
		if op.Kind == "generate" && op.Transport == p.SSE {
			operation = op
		}
	}
	candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, model: config.ModelRef{Name: "m", BaseURL: provider.URL}, binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []p.Transport{p.SSE}}}
	request, err := upstream.DecodeRequest(t.Context(), []byte("{\"model\":\"m\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
	if err != nil || result == nil {
		t.Fatalf("enabled startup rule failed: %v", err)
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
}
