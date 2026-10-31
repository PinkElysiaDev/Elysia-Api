package protocol

import (
	"strings"
	"testing"
)

func TestPackageStreamRetainsCapturedSource(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.Native.Preserve = false
	mapping := document.Mapping.Directions[DecodeEvent]
	mapping.Indexes = map[string]ProtocolStateIndex{"origins": {KeyMode: "string", Policy: "last", Schema: ValueSchema{Type: ObjectType, AllowUnknown: true}, CanRetainSource: true}}
	key := readExpression("input", "/item_id")
	isStart := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(ItemStarted)}}
	isReplacement := Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/replacement"))}
	source := Expression{Op: "capture_source", Source: expressionPointer(readExpression("input", "/detail"))}
	store := Expression{Op: "if", When: &isStart,
		Then:      &Expression{Op: "remember", Name: "origins", Source: &key, Body: expressionPointer(objectExpression(map[string]Expression{"source": source}))},
		Otherwise: &Expression{Op: "if", When: &isReplacement, Then: &Expression{Op: "remember", Name: "origins", Source: &key, Body: expressionPointer(readExpression("input", "/replacement"))}}}
	mapping.Program.Fields["item"] = Expression{Op: "if", When: &Expression{Op: "not", Source: &isReplacement}, Then: &Expression{Op: "let", Name: "origin", Source: &Expression{Op: "recall", Name: "origins", Source: &key, Policy: "reject"}, Body: expressionPointer(objectExpression(map[string]Expression{
		"kind": literalExpression(TextNode), "source": {Op: "read", From: "binding", Name: "origin", Path: "/source"},
	}))}}
	body := mapping.Program
	mapping.Program = Expression{Op: "let", Name: "stored", Source: &store, Body: &body}
	document.Mapping.Directions[DecodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState(), Scope: Scope{Provider: "fixture", Account: "account", Model: "m"}}
	defer options.State.Release()
	first := primitiveFrame(t, map[string]any{"kind": ItemStarted, "detail": map[string]any{"payload": strings.Repeat("x", 128<<10)}})
	events, err := compiled.DecodeEvents(t.Context(), first, options)
	if err != nil || len(events) != 1 || events[0].Item.Source == nil || events[0].Item.Source.Path != "/detail" {
		t.Fatal("initial capture lost its original location", events, err)
	}
	origin := *events[0].Item.Source
	events, err = compiled.DecodeEvents(t.Context(), primitiveFrame(t, map[string]any{"text": "delta"}), options)
	if err != nil || len(events) != 1 || events[0].Item.Source == nil || *events[0].Item.Source != origin {
		t.Fatal("later frame lost the captured source", events, err)
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}].indexes[0]["i"]
	if strings.Contains(retained.raw, strings.Repeat("x", 64)) {
		t.Fatal("provenance retained the captured frame payload")
	}
	// Same-looking provider JSON has no capture receipt, including when it
	// replaces an entry that previously held a valid captured source.
	replacement := primitiveFrame(t, map[string]any{"kind": MetadataUpdated, "replacement": map[string]any{"source": origin}})
	if _, err := compiled.DecodeEvents(t.Context(), replacement, options); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeEvents(t.Context(), primitiveFrame(t, map[string]any{"text": "next"}), options); err == nil {
		t.Fatal("ordinary JSON inherited an overwritten source receipt")
	}
	entry := document.Mapping.Directions[DecodeEvent]
	index := entry.Indexes["origins"]
	index.CanRetainSource = false
	entry.Indexes["origins"] = index
	document.Mapping.Directions[DecodeEvent] = entry
	plain := compilePackageForTest(t, document)
	plainOptions := EvaluationContext{State: NewEvaluationState(), Scope: options.Scope}
	defer plainOptions.State.Release()
	if _, err := plain.DecodeEvents(t.Context(), first, plainOptions); err == nil {
		t.Fatal("ordinary index unexpectedly preserved source authority")
	}
}
