package protocol

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestMappingCancellationRetainsCause(t *testing.T) {
	compiled := &Compiled{}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		cause := context.Canceled
		expectedCode := ExecutionCancelled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			cause = context.DeadlineExceeded
			expectedCode = ExecutionTimeout
		} else {
			cancel()
		}
		defer cancel()
		_, err := compiled.executeMapping(ctx, EncodeEvent, StringValue("test"), EvaluationContext{})
		if !errors.Is(err, cause) || !errors.Is(compiled.runtimeError(EncodeEvent, err), cause) {
			t.Fatalf("cause lost: %v", err)
		}
		var conversion *ConversionError
		if errors.As(compiled.runtimeError(EncodeEvent, err), &conversion) {
			t.Fatal("cancellation became an input error")
		}
		_, issues := compiled.Execute(ctx, EncodeEvent, StringValue("test"), EvaluationContext{})
		if len(issues) != 1 || issues[0].Code != expectedCode || issues[0].Suggestion != "" || !slices.Contains(DiagnosticCatalog(), expectedCode) {
			t.Fatalf("incorrect report: %+v", issues)
		}
	}
}
