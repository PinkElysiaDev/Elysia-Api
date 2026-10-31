package protocol

import "testing"

func TestVerificationBindsStableOperationMetadata(t *testing.T) {
	document := eventProtocolPackage(t)
	encoder := document.Mapping.Directions[EncodeEvent].Program.Fields
	encoder["execution"] = readExpression("runtime", "/operation/id")
	encoder["started_at"] = readExpression("runtime", "/operation/startedAt")
	compiled := compilePackageForTest(t, document)
	input := fixtureValue(t, []Event{
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: coreInteger(0)},
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r"), Sequence: coreInteger(1)},
	})
	sample := Sample{ID: "operation-context", Direction: EncodeEvent, Sequence: true, Input: input, Context: Object{"operation": Value{raw: `{"id":"untrusted","startedAt":999}`}}}
	result, err := executeVerificationSample(t.Context(), compiled, sample)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := readArray(result.output)
	if err != nil || len(frames) != 2 {
		t.Fatal(frames, err)
	}
	for _, frame := range frames {
		id, err := readValuePointer(frame, []string{"execution"})
		if err != nil || !id.IsString(verificationOperationID) {
			t.Fatal("context JSON replaced the operation identity", id, err)
		}
		startedAt, err := readValuePointer(frame, []string{"started_at"})
		if err != nil || startedAt != coreInteger(verificationStartedAt) {
			t.Fatal("frames did not share the offline start time", startedAt, err)
		}
	}
	if err := verifyEventRoundTrip(t.Context(), compiled, sample, result); err != nil {
		t.Fatal(err)
	}
}
