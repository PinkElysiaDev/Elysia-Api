package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMissingCoreMappingDiagnostics(t *testing.T) {
	const reason = "direction is not implemented"
	compiled := &Compiled{identity: Identity{DefinitionID: "missing-direction"}, limits: DefaultLimits()}
	input := Value{raw: `null`}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, stop := context.WithDeadline(t.Context(), time.Time{})
	defer stop()
	for _, direction := range DirectionCatalog() {
		t.Run(string(direction), func(t *testing.T) {
			call := func(ctx context.Context) error {
				switch direction {
				case DecodeRequest:
					_, err := decodeCore[Request](ctx, compiled, direction, input, EvaluationContext{})
					return err
				case DecodeResponse:
					_, err := decodeCore[Response](ctx, compiled, direction, input, EvaluationContext{})
					return err
				case DecodeEvent, DecodeClientEvent:
					_, err := compiled.decodeEventValues(ctx, direction, input, EvaluationContext{})
					return err
				case EncodeRequest:
					_, err := encodeCore(ctx, compiled, direction, &Request{}, EvaluationContext{})
					return err
				case EncodeResponse:
					_, err := encodeCore(ctx, compiled, direction, &Response{}, EvaluationContext{})
					return err
				default:
					_, err := compiled.encodeEventValue(ctx, direction, Event{}, EvaluationContext{})
					return err
				}
			}
			for _, execute := range []func(context.Context) error{call, func(ctx context.Context) error {
				_, err := compiled.executeMapping(ctx, direction, input, EvaluationContext{})
				return err
			}} {
				err := execute(t.Context())
				var failure *ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 {
					t.Fatal("missing direction lost structured diagnostics", err)
				}
				code := InvalidInput
				if direction == DecodeResponse || direction == DecodeEvent {
					code = UpstreamContractViolation
				}
				issue := failure.Issues[0]
				cause := errors.Unwrap(failure)
				if issue.Code != code || issue.Path != "/directions/"+string(direction) || issue.Stage != "evaluate" || issue.Protocol != compiled.identity || issue.Direction != direction || issue.Reason != reason || cause == nil || cause.Error() != reason {
					t.Fatal("missing direction changed its cause or location", issue, cause)
				}
				for _, fixture := range []struct {
					ctx context.Context
					err error
				}{{cancelled, context.Canceled}, {expired, context.DeadlineExceeded}} {
					if err := execute(fixture.ctx); !errors.Is(err, fixture.err) {
						t.Fatal("missing direction replaced cancellation", err)
					}
				}
			}
		})
	}
}

func TestMissingCoreMappingPreservesSemanticBudgetPriority(t *testing.T) {
	const bodyLimit = 64
	compiled := &Compiled{limits: DefaultLimits()}
	ctx, err := WithRequestBodyLimit(t.Context(), bodyLimit)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	request := &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("x", bodyLimit*semanticByteMultiplier))}}}
	for _, ctx := range []context.Context{ctx, cancelled} {
		_, err := encodeCore(ctx, compiled, EncodeRequest, request, EvaluationContext{})
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Domain != "semantic" || failure.Metric != "bytes" || failure.Limit != bodyLimit*semanticByteMultiplier || errors.Is(err, context.Canceled) {
			t.Fatal("missing direction or cancellation replaced semantic budget failure", err)
		}
	}
}

func TestMappingTransactionErrorPrecedesCancellation(t *testing.T) {
	compiled := compilePackageForTest(t, statefulEventPackage(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := compiled.executeMapping(ctx, DecodeEvent, Value{raw: `null`}, EvaluationContext{})
	var failure *ConversionError
	if !errors.As(err, &failure) || errors.Is(err, context.Canceled) || failure.Issues[0].Reason != "declared event state requires one EvaluationState per stream" {
		t.Fatal("cancellation replaced the transaction prerequisite", err)
	}
}
