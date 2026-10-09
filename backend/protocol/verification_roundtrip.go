package protocol

import "context"

func verifyRoundTrip(ctx context.Context, compiled *Compiled, sample Sample, result verificationResult) error {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	var semantic any
	var wire Value
	switch sample.Direction {
	case DecodeRequest:
		if !compiled.Supports(EncodeRequest) {
			return nil
		}
		body, err := compiled.EncodeRequest(ctx, result.semantic.(*Request), options)
		if err != nil {
			return err
		}
		wire, err = ParseValue(body)
		if err != nil {
			return err
		}
		semantic, err = compiled.DecodeRequest(ctx, body, options)
		if err != nil {
			return err
		}
	case EncodeRequest:
		if !compiled.Supports(DecodeRequest) {
			return nil
		}
		var err error
		semantic, err = compiled.DecodeRequest(ctx, result.output.Bytes(), options)
		if err != nil {
			return err
		}
	case DecodeResponse:
		if !compiled.Supports(EncodeResponse) {
			return nil
		}
		body, err := compiled.EncodeResponse(ctx, result.semantic.(*Response), options)
		if err != nil {
			return err
		}
		wire, err = ParseValue(body)
		if err != nil {
			return err
		}
		semantic, err = compiled.DecodeResponse(ctx, body, options)
		if err != nil {
			return err
		}
	case EncodeResponse:
		if !compiled.Supports(DecodeResponse) {
			return nil
		}
		var err error
		semantic, err = compiled.DecodeResponse(ctx, result.output.Bytes(), options)
		if err != nil {
			return err
		}
	case DecodeEvent, EncodeEvent, DecodeClientEvent, EncodeUpstreamEvent:
		return verifyEventRoundTrip(ctx, compiled, sample, result)
	}
	if !wire.IsZero() && compiled.native.Preserve && !equalValues(sample.Input, wire) {
		return IssuesError([]ConversionIssue{verificationIssue(compiled, sample.Direction, "/native"+differencePath(sample.Input, wire), VerificationMismatch, "same-wire roundtrip changed native fields", sample.ID)})
	}
	return compareRoundTrip(compiled, sample, result.semantic, semantic)
}

const nativeProbeField = "__elysia_native_verification__"

func verifyNativeExtension(ctx context.Context, compiled *Compiled, sample Sample) error {
	if sample.Direction == EncodeRequest || sample.Direction == EncodeResponse {
		result, err := executeVerificationSample(ctx, compiled, sample)
		if err != nil {
			return err
		}
		sample.Input = result.output
		if sample.Direction == EncodeRequest {
			sample.Direction = DecodeRequest
		} else {
			sample.Direction = DecodeResponse
		}
	}
	input, err := sample.Input.ReadObject()
	if err != nil {
		return err
	}
	probe, err := ParseValue([]byte(`{"null":null,"false":false,"zero":0,"long":900719925474099312345,"array":[{},[],null,false]}`))
	if err != nil {
		return err
	}
	if _, exists := input[nativeProbeField]; exists {
		return IssuesError([]ConversionIssue{verificationIssue(compiled, sample.Direction, "/native", InvalidInput, "fixture collides with the engine's native extension probe field", sample.ID)})
	}
	input[nativeProbeField] = probe
	sample.Input, err = EncodeValue(input)
	if err != nil {
		return err
	}
	result, err := executeVerificationSample(ctx, compiled, sample)
	if err != nil {
		return err
	}
	var native *Native
	switch semantic := result.semantic.(type) {
	case *Request:
		native = semantic.Native
	case *Response:
		native = semantic.Native
	}
	if native == nil || !equalValues(native.Value, sample.Input) {
		return IssuesError([]ConversionIssue{verificationIssue(compiled, sample.Direction, "/native", VerificationMismatch, "native extension was not preserved by the decoder", sample.ID)})
	}
	return verifyRoundTrip(ctx, compiled, sample, result)
}

func verifyEventRoundTrip(ctx context.Context, compiled *Compiled, sample Sample, result verificationResult) error {
	if !compiled.Supports(eventDecoder(sample.Direction)) || !compiled.Supports(eventEncoder(sample.Direction)) {
		return nil
	}
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	var frames []Value
	if isEventDecoder(sample.Direction) {
		for _, frame := range result.frames {
			encoded, err := compiled.encodeFrame(ctx, eventEncoder(sample.Direction), frame, options)
			if err != nil {
				return err
			}
			frames = append(frames, encoded...)
		}
		if sample.Sequence {
			tail, err := compiled.finishEvents(ctx, eventEncoder(sample.Direction), options)
			if err != nil {
				return err
			}
			frames = append(frames, tail...)
		}
		if compiled.native.Preserve {
			var preserved Value
			if sample.Sequence {
				var err error
				preserved, err = EncodeValue(frames)
				if err != nil {
					return err
				}
			} else if len(frames) == 1 {
				preserved = frames[0]
			}
			if !equalValues(sample.Input, preserved) {
				return IssuesError([]ConversionIssue{verificationIssue(compiled, sample.Direction, "/native", VerificationMismatch, "same-wire event roundtrip changed frame values or count", sample.ID)})
			}
		}
	} else if sample.Sequence || compiled.mappings[sample.Direction].frameBatch {
		var err error
		frames, err = readArray(result.output)
		if err != nil {
			return err
		}
	} else {
		frames = []Value{result.output}
	}
	events := []Event{}
	for _, frame := range frames {
		decoded, err := compiled.decodeFrame(ctx, eventDecoder(sample.Direction), frame, options)
		if err != nil {
			return err
		}
		events = append(events, decoded.Events...)
	}
	if compiled.native.Preserve && isEventDecoder(sample.Direction) {
		return compareRoundTrip(compiled, sample, result.semantic, events)
	}
	return compareEventSequence(compiled, sample, result.semantic.([]Event), events)
}

func compareRoundTrip(compiled *Compiled, sample Sample, expected, actual any) error {
	before, err := comparableSemantic(expected)
	if err != nil {
		return err
	}
	after, err := comparableSemantic(actual)
	if err != nil {
		return err
	}
	if equalValues(before, after) {
		return nil
	}
	issue := verificationIssue(compiled, sample.Direction, "/roundtrip"+differencePath(before, after), VerificationMismatch, "roundtrip lost or changed semantic content", sample.ID)
	return IssuesError([]ConversionIssue{issue})
}

// CanActivate requires a current, passing offline report. Online evidence is
// deliberately unable to satisfy the offline activation prerequisite.
func CanActivate(compiled *Compiled, report VerificationReport) []ConversionIssue {
	if report.Kind == OfflineVerification && report.Passed && len(report.Checks) > 0 && IssuesError(report.Issues) == nil && report.IsCurrent(compiled.hash, CompilerVersion, compiled.samplesHash) {
		return nil
	}
	issues := []ConversionIssue{verificationIssue(compiled, "", "/verification", VerificationRequired, "activation requires passing offline evidence bound to this definition, compiler and sample set", "")}
	if report.IsCurrent(compiled.hash, CompilerVersion, compiled.samplesHash) {
		issues = append(issues, report.Issues...)
	}
	return issues
}
