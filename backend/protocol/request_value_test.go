package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRequestValueBoundariesPreserveValidationAndIsolation(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("values", "model", "history", "tools", "answer"))
	body := []byte(`{"model":"m","history":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}]}]}`)
	value, _ := ParseValue(body)
	fromBytes, err := compiled.DecodeRequest(t.Context(), body, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	fromValue, err := compiled.DecodeRequestValue(t.Context(), value, EvaluationContext{})
	if err != nil || !reflect.DeepEqual(fromBytes, fromValue) {
		t.Fatalf("decode parity: %v", err)
	}
	fromValue.Content[0].Children[0].Payload = StringValue("changed")
	if fromBytes.Content[0].Children[0].Payload.IsString("changed") || strings.Contains(value.JSONString(), "changed") {
		t.Fatal("value decoding shared mutable candidate containers")
	}
	wireBytes, err := compiled.EncodeRequest(t.Context(), fromBytes, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	wireValue, err := compiled.EncodeRequestValue(t.Context(), fromBytes, EvaluationContext{})
	if err != nil || string(wireBytes) != wireValue.JSONString() {
		t.Fatalf("encode parity: %v", err)
	}
	wireBytes[0] = '['
	if !wireValue.IsObject() {
		t.Fatal("public byte encoding exposed immutable result storage")
	}
	for _, ctx := range []context.Context{t.Context(), func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }()} {
		for _, name := range []string{"generate", "missing"} {
			before := compiled.CheckOperationInput(ctx, name, value.Bytes())
			after := compiled.CheckOperationValueInput(ctx, name, value)
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("operation parity (%s): %v / %v", name, before, after)
			}
		}
	}
	if _, err := compiled.DecodeRequestValue(t.Context(), Value{}, EvaluationContext{}); err == nil {
		t.Fatal("absent document accepted")
	}
	limited, _ := WithRequestBodyLimit(t.Context(), 16)
	_, before := compiled.DecodeRequest(limited, body, EvaluationContext{})
	_, after := compiled.DecodeRequestValue(limited, value, EvaluationContext{})
	if before == nil || fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("immutable input bypassed decode budget: %v / %v", before, after)
	}
	denied := errors.New("denied by scope resolver")
	_, err = compiled.DecodeRequestValue(t.Context(), value, EvaluationContext{ResolveRequestScope: func(*Request) (Scope, error) { return Scope{}, denied }})
	if !errors.Is(err, denied) {
		t.Fatal("immutable input bypassed scope resolution", err)
	}
}
