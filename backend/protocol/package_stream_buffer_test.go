package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func bufferPackage(t *testing.T, kind string) *Compiled {
	t.Helper()
	branches := map[string]Expression{}
	for _, operation := range []string{"append", "snapshot", "read", "length", "release"} {
		expression := Expression{Op: kind, Name: "payload", Operation: operation, Source: expressionPointer(readExpression("input", "/item_id"))}
		if operation == "append" || operation == "snapshot" {
			expression.Body = expressionPointer(readExpression("input", "/value"))
		}
		switch {
		case operation == "release":
			source := expression
			expression = Expression{Op: "let", Name: "released", Source: &source, Body: expressionPointer(literalExpression(""))}
		case kind == "buffer_array" || operation == "length":
			source := expression
			expression = Expression{Op: "stringify_json", Source: &source}
		}
		branches[operation] = expression
	}
	return streamPrimitivePackage(t, Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/mode")), Cases: branches})
}

func TestPackageBufferTextTransactionsAndSnapshots(t *testing.T) {
	compiled := bufferPackage(t, "buffer_text")
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	apply := func(mode, value string, isInvalid bool) (string, error) {
		fields := map[string]any{"mode": mode, "value": value}
		if isInvalid {
			fields["kind"] = "invalid-event"
		}
		return decodePrimitiveText(t, compiled, options, fields)
	}
	for _, value := range []string{"a\n", "猫"} {
		if got, err := apply("append", value, false); err != nil || got != value {
			t.Fatal(got, err)
		}
	}
	first, err := apply("read", "", false)
	if err != nil || first != "a\n猫" {
		t.Fatal(first, err)
	}
	if _, err := apply("append", "discard", true); err == nil {
		t.Fatal("failed event committed a buffer append")
	}
	if got, err := apply("snapshot", "a\n猫!", false); err != nil || got != "!" {
		t.Fatal(got, err)
	}
	if _, err := apply("snapshot", "changed", false); err == nil {
		t.Fatal("rewritten prefix accepted")
	}
	if got, err := apply("read", "", false); err != nil || got != "a\n猫!" || first != "a\n猫" {
		t.Fatal("snapshot mutated or rollback lost content", got, err)
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}]
	buffer := retained.primitives.buffers[streamTrackKey{channel: "buffer_text:payload", item: "i"}]
	if len(buffer.fragments) != 1 {
		t.Fatal("materialized text retained its old fragments")
	}
	if _, err := apply("release", "", true); err == nil {
		t.Fatal("failed event released retained state")
	}
	if got, err := apply("length", "", false); err != nil || got != "6" {
		t.Fatal(got, err)
	}
	if _, err := apply("release", "", false); err != nil {
		t.Fatal(err)
	}
	retained = options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}]
	if len(retained.primitives.buffers) != 0 || len(retained.primitives.trackedItems) != 0 || options.State.packageStateBytes != 0 {
		t.Fatal("release retained payload or identity budget")
	}
	if _, err := apply("read", "", false); err == nil {
		t.Fatal("read recreated a released buffer")
	}
}

func TestPackageBufferArrayPreservesExactSnapshots(t *testing.T) {
	compiled := bufferPackage(t, "buffer_array")
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	apply := func(mode, value string, isInvalid bool) (string, error) {
		fields := map[string]any{"mode": mode, "value": json.RawMessage(value)}
		if isInvalid {
			fields["kind"] = "invalid-event"
		}
		return decodePrimitiveText(t, compiled, options, fields)
	}
	if _, err := apply("append", `[{"n":9007199254740993,"s":"u"}]`, false); err != nil {
		t.Fatal(err)
	}
	if got, err := apply("snapshot", `[{"s":"\u0075","n":9007199254740993.0},null]`, false); err != nil || got != `[null]` {
		t.Fatal(got, err)
	}
	before, err := apply("read", `[]`, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apply("append", `[false]`, true); err == nil {
		t.Fatal("failed event committed array values")
	}
	for _, value := range []string{`[{"n":9007199254740992,"s":"u"},null]`, `[]`} {
		if _, err := apply("snapshot", value, false); err == nil {
			t.Fatal("changed or shortened array accepted")
		}
	}
	if got, err := apply("read", `[]`, false); err != nil || got != before {
		t.Fatal("failed operations changed a retained array", got, err)
	}
	if got, err := apply("length", `[]`, false); err != nil || got != "2" {
		t.Fatal(got, err)
	}
	if _, err := apply("release", `[]`, false); err != nil {
		t.Fatal(err)
	}
	if options.State.packageStateBytes != 0 {
		t.Fatal("array release retained payload bytes")
	}
}

func TestPackageBufferRetentionLimit(t *testing.T) {
	compiled := bufferPackage(t, "buffer_text")
	compiled.limits.BufferBytes = 256
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for range 15 {
		if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"mode": "append", "value": strings.Repeat("x", 16)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := decodePrimitiveText(t, compiled, options, map[string]any{"mode": "append", "value": strings.Repeat("x", 16)})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Domain != "stream" || limit.Metric != "state_bytes" {
		t.Fatal("retained payload escaped the shared stream budget", err)
	}
}
