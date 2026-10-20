package protocol

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestTypedEventConversionBoundaries(t *testing.T) {
	value, _ := ParseValue([]byte(` { "n": 9007199254740993, "text": "<>&" } `))
	event := Event{SchemaVersion: 1, Source: Identity{Family: "invalid\xff"}, Type: ItemStarted, Item: &Node{Kind: TextNode, Payload: value, Children: []Node{}, Native: &Native{Value: value}}}
	for _, mode := range []string{"normalize", "authored", "limit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			policy := ConversionPolicy{SchemaVersion: 1, ID: "test"}
			ctx := t.Context()
			switch mode {
			case "authored":
				policy.Rules = []ConversionRule{{ID: "edit", Enabled: true, Phase: ConversionEvent, Action: "set", Path: "/delta", Value: StringValue("edited")}}
			case "limit":
				limits := DefaultLimits()
				limits.Nodes = 2
				policy.Limits = &limits
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			conversion, err := CompileConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			input, err := EncodeSemantic(ctx, DecodeEvent, event)
			if err != nil {
				t.Fatal(err)
			}
			old, oldErr := conversion.ApplyValue(ctx, ConversionEvent, input, ConversionContext{}, nil)
			actual, newErr := conversion.Event(ctx, event, ConversionContext{}, nil)
			if fmt.Sprint(oldErr) != fmt.Sprint(newErr) {
				t.Fatalf("boundary errors differ: %v / %v", oldErr, newErr)
			}
			if oldErr != nil {
				return
			}
			var expected Event
			if err := decodeContract(old.Bytes(), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("normalization differs: %#v / %#v", expected, actual)
			}
			actual.Item.Native.Source.Path = "private"
			actual.Item.Payload = StringValue("private")
			if event.Item.Native.Source.Path != "" || event.Item.Payload != value {
				t.Fatal("typed result aliases input")
			}
		})
	}
}
