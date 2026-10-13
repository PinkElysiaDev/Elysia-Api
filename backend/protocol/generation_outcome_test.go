package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestDeclaredFailureCannotBecomeSuccessfulGeneration(t *testing.T) {
	for _, response := range []*Response{
		{SchemaVersion: 1, Error: StringValue("quota exhausted")},
		{SchemaVersion: 1, Status: StringValue("failed")},
		{SchemaVersion: 1, Status: StringValue("cancelled")},
	} {
		var failure *GenerationFailure
		if err := CheckGenerationOutcome(response); !errors.As(err, &failure) {
			t.Fatal("failure accepted as success", err)
		}
		collector, err := NewResponseCollector(Target{}, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = collector.Consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: response})
		if !errors.As(err, &failure) {
			t.Fatal("collector accepted failed terminal", err)
		}
	}
	null, _ := ParseValue([]byte("null"))
	if err := CheckGenerationOutcome(&Response{Error: null, Status: StringValue("completed")}); err != nil {
		t.Fatal(err)
	}
	if err := CheckGenerationOutcome(&Response{Error: StringValue("quota exhausted")}); !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatal(err)
	}
}

func TestGenerationFailureExcludesOpaquePayloadFromMessage(t *testing.T) {
	payload, _ := ParseValue([]byte(`{"category":"rate_limit","message":"synthetic limit","code":"gateway_concurrency_limit","details":{"private":"must-not-leak"},"wire:vendor":{"token":"also-private"}}`))
	err := &GenerationFailure{Payload: payload}
	if !strings.Contains(err.Error(), "gateway_concurrency_limit") || strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "also-private") {
		t.Fatal(err)
	}
}
