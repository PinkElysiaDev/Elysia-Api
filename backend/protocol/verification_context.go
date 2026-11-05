package protocol

const (
	verificationOperationID       = "verification-execution"
	verificationStartedAt   int64 = 1
)

var verificationOperation = operationMetadata{id: StringValue(verificationOperationID), startedAt: coreInteger(verificationStartedAt)}

// Offline execution uses explicit immutable synthetic facts. They remain the
// same through the fixture, target rendering and roundtrip, and cannot be
// supplied or replaced by a sample's context JSON.
func newVerificationContext(scope Scope, values Object) EvaluationContext {
	return EvaluationContext{Scope: scope, Values: values, State: NewEvaluationState(), operation: &verificationOperation}
}
