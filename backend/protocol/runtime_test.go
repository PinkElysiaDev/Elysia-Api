package protocol

import (
	"strings"
	"testing"
)

func TestRuntimeNativePreservationAndSemanticEdits(t *testing.T) {
	definition := testDefinition("native", "model", "history", "functions", "answer")
	definition.Native.Preserve = true
	compiled := compileTestDefinition(t, definition)
	input := []byte(`{"model":"old","extension":{"long":900719925474099312345,"null":null},"history":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}],"vendor":false}],"functions":[{"operation":"lookup","schema":{"type":"object"},"vendor":{"ttl":"1h"}}]}`)
	request, err := compiled.DecodeRequest(t.Context(), input, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ParseValue(input)
	got, _ := ParseValue(roundtrip)
	if !equalValues(want, got) {
		t.Fatalf("native roundtrip: %s", roundtrip)
	}
	request.Model = StringValue("new")
	request.Tools[0].Name = StringValue("renamed")
	modified, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(modified), `"operation":"renamed"`) || !strings.Contains(string(modified), `"ttl":"1h"`) || !strings.Contains(string(modified), `"model":"new"`) {
		t.Fatalf("semantic edit lost: %s", modified)
	}
	request.Tools = nil
	cleared, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cleared), `"functions"`) {
		t.Fatalf("deleted tools resurrected: %s", cleared)
	}
	foreign := compileTestDefinition(t, testDefinition("foreign", "model", "history", "functions", "answer"))
	converted, err := foreign.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(converted), `"extension"`) || strings.Contains(string(converted), `"vendor"`) {
		t.Fatalf("native fields escaped to another family: %s", converted)
	}
}

func TestNativeArrayReorderUsesDeclaredIdentity(t *testing.T) {
	original, _ := ParseValue([]byte(`{"items":[{"id":"a","text":"one","vendor":1},{"id":"b","text":"two","vendor":2}]}`))
	before, _ := ParseValue([]byte(`{"items":[{"id":"a","text":"one"},{"id":"b","text":"two"}]}`))
	after, _ := ParseValue([]byte(`{"items":[{"id":"b","text":"changed two"},{"id":"a","text":"changed one"}]}`))
	if _, err := reconcileNative(t.Context(), original, before, after, NativePolicy{Preserve: true}, DefaultLimits()); err == nil {
		t.Fatal("ambiguous edits silently matched by position")
	}
	result, err := reconcileNative(t.Context(), original, before, after, NativePolicy{Preserve: true, ArrayKeys: map[string]string{"/items": "/id"}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ParseValue([]byte(`{"items":[{"id":"b","text":"changed two","vendor":2},{"id":"a","text":"changed one","vendor":1}]}`))
	if !equalValues(result, want) {
		t.Fatalf("reordered extensions: %s", result.Bytes())
	}
}

func TestRuntimeStampsAllResourceScopes(t *testing.T) {
	scope := Scope{Provider: "p", Account: "a", Model: "m"}
	resource := func() *Resource { return &Resource{Kind: "cache", ID: StringValue("c")} }
	request := &Request{Cache: []CacheIntent{{Resource: resource()}}, Tools: []Tool{{Cache: []CacheIntent{{Resource: resource()}}}}, Content: []Node{{Cache: []CacheIntent{{Resource: resource()}}, Children: []Node{{Resources: []Resource{*resource()}}}}}}
	if err := stampResourceScopes(request, scope); err != nil {
		t.Fatal(err)
	}
	for _, got := range []Scope{request.Cache[0].Resource.Scope, request.Tools[0].Cache[0].Resource.Scope, request.Content[0].Cache[0].Resource.Scope, request.Content[0].Children[0].Resources[0].Scope} {
		if got != scope {
			t.Fatalf("unstamped resource: %+v", got)
		}
	}
	request.Cache[0].Resource.Scope.Account = "other"
	if err := stampResourceScopes(request, scope); err == nil {
		t.Fatal("mapping replaced gateway-owned scope")
	}
}

func TestRuntimeRejectsInvalidResponseAndEvents(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("bad-response", "m", "h", "t", "r"))
	if _, err := compiled.DecodeResponse(t.Context(), []byte(`{"r":[{"kind":"imaginary"}]}`), EvaluationContext{}); err == nil {
		t.Fatal("unknown semantic content accepted")
	}
	for _, frame := range []string{`{"event":"token","token":"x"}`, `{"event":"unknown","token":"x","item":"i"}`} {
		input, _ := ParseValue([]byte(frame))
		if _, err := compiled.DecodeEvents(t.Context(), input, EvaluationContext{}); err == nil {
			t.Fatalf("invalid event accepted: %s", frame)
		}
	}
}

func TestRuntimeNativeEventPreservation(t *testing.T) {
	definition := testDefinition("event-native", "m", "h", "t", "r")
	definition.Native.Preserve = true
	compiled := compileTestDefinition(t, definition)
	frame, _ := ParseValue([]byte(`{"event":"token","item":"i","token":"before","extension":9007199254740993123}`))
	events, err := compiled.DecodeEvents(t.Context(), frame, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	events[0].Delta = StringValue("after")
	result, err := compiled.EncodeEvent(t.Context(), events[0], EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Bytes()), `"token":"after"`) || !strings.Contains(string(result.Bytes()), `9007199254740993123`) {
		t.Fatalf("native event edit: %s", result.Bytes())
	}
}
