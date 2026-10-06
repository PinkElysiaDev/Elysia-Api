package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

type verificationResult struct {
	output   Value
	semantic any
	frames   []*EventFrame
}

// Verify executes the revision's offline fixtures through the typed runtime,
// verifies reversible mappings, and requires evidence for declared features.
// It performs no network I/O and can never produce upstream verification.
func Verify(ctx context.Context, compiled *Compiled) VerificationReport {
	report := VerificationReport{DefinitionHash: compiled.hash, CompilerVersion: CompilerVersion, SamplesHash: compiled.samplesHash, Kind: OfflineVerification, VerifiedAt: time.Now().UTC(), Checks: []VerificationCheck{}, Issues: []ConversionIssue{}}
	coverage := map[Direction]CapabilitySet{}
	hasSample, hasSequence := map[Direction]bool{}, map[Direction]bool{}
	hasLifecycle := map[Direction]map[Capability]bool{}
	operationEvidence := map[string]bool{}
	for _, direction := range DirectionCatalog() {
		coverage[direction], hasLifecycle[direction] = CapabilitySet{}, map[Capability]bool{}
	}
	for index, sample := range compiled.Definition().Samples {
		path := fmt.Sprintf("/samples/%d", index)
		check := VerificationCheck{SampleID: sample.ID, Direction: sample.Direction}
		result, err := executeVerificationSample(ctx, compiled, sample)
		if err == nil && (sample.Direction == DecodeRequest || sample.Direction == EncodeRequest) {
			wire := sample.Input
			if sample.Direction == EncodeRequest {
				wire = result.output
			}
			for _, name := range sortedKeys(compiled.operations) {
				operation := compiled.operations[name]
				if operation.Kind != "generate" || (sample.Operation != "" && sample.Operation != name) {
					continue
				}
				if err = compiled.CheckOperationInput(operation, wire.Bytes()); err != nil {
					break
				}
			}
		}
		if sample.ExpectedIssue != "" {
			check.Passed = hasIssueCode(err, sample.ExpectedIssue)
			if !check.Passed {
				report.Issues = append(report.Issues, verificationIssue(compiled, sample.Direction, path, VerificationMismatch, "sample did not produce its expected diagnostic", sample.ID))
			}
			report.Checks = append(report.Checks, check)
			continue
		}
		if err != nil {
			report.Issues = append(report.Issues, sampleIssues(compiled, sample, path, err)...)
			report.Checks = append(report.Checks, check)
			continue
		}
		expected, err := comparableExpected(sample)
		if err != nil {
			report.Issues = append(report.Issues, verificationIssue(compiled, sample.Direction, path+"/expected", InvalidInput, "invalid expected semantic contract: "+err.Error(), sample.ID))
			report.Checks = append(report.Checks, check)
			continue
		}
		if !equalValues(result.output, expected) {
			report.Issues = append(report.Issues, verificationIssue(compiled, sample.Direction, path+"/expected"+differencePath(expected, result.output), VerificationMismatch, "actual output differs from the expected fixture", sample.ID))
			report.Checks = append(report.Checks, check)
			continue
		}
		if err := verifyRoundTrip(ctx, compiled, sample, result); err != nil {
			report.Issues = append(report.Issues, sampleIssues(compiled, sample, path, err)...)
			report.Checks = append(report.Checks, check)
			continue
		}
		evidence := observeCapabilities(result.semantic)
		if compiled.mappings[sample.Direction].capabilities[NativeExtensionsCapability] && compiled.native.Preserve &&
			!isEventDirection(sample.Direction) {
			if err := verifyNativeExtension(ctx, compiled, sample); err != nil {
				report.Issues = append(report.Issues, sampleIssues(compiled, sample, path, err)...)
			} else {
				evidence.observed[NativeExtensionsCapability] = true
			}
		}
		hasInvalidClaim := false
		for _, capability := range sample.Capabilities {
			if !evidence.observed[capability] {
				hasInvalidClaim = true
				report.Issues = append(report.Issues, verificationIssue(compiled, sample.Direction, path+"/capabilities", IncompleteCoverage, "claimed sample capability has no semantic witness: "+string(capability), sample.ID))
			}
		}
		if hasInvalidClaim {
			report.Checks = append(report.Checks, check)
			continue
		}
		for _, capability := range CapabilityCatalog() {
			if evidence.observed[capability] {
				coverage[sample.Direction][capability] = true
				check.Capabilities = append(check.Capabilities, capability)
			}
		}
		// The frame roundtrip above executes the pinned target encoder's native
		// preservation path. It is also positive output evidence for extensions;
		// fabricating a stand-alone semantic event cannot exercise compound frames.
		if isEventDecoder(sample.Direction) && compiled.native.Preserve && evidence.observed[NativeExtensionsCapability] && compiled.Supports(eventEncoder(sample.Direction)) {
			coverage[eventEncoder(sample.Direction)][NativeExtensionsCapability] = true
		}
		hasSample[sample.Direction] = true
		if sample.Direction == DecodeRequest || sample.Direction == EncodeRequest {
			for name, operation := range compiled.operations {
				if operation.Kind == "generate" && (sample.Operation == "" || sample.Operation == name) {
					operationEvidence[name] = true
				}
			}
		}
		hasSequence[sample.Direction] = hasSequence[sample.Direction] || sample.Sequence
		hasLifecycle[sample.Direction][FunctionToolsCapability] = hasLifecycle[sample.Direction][FunctionToolsCapability] || evidence.hasFunctionLifecycle
		hasLifecycle[sample.Direction][FreeTextToolsCapability] = hasLifecycle[sample.Direction][FreeTextToolsCapability] || evidence.hasFreeTextLifecycle
		check.Passed = true
		report.Checks = append(report.Checks, check)
	}
	for _, name := range sortedKeys(compiled.operations) {
		if compiled.operations[name].Input != nil && !operationEvidence[name] {
			report.Issues = append(report.Issues, verificationIssue(compiled, DecodeRequest, "/operations/"+name+"/input", IncompleteCoverage, "operation input schema requires a passing request fixture", ""))
		}
	}
	verifySessionSamples(ctx, compiled, &report, coverage, hasSample, hasSequence, hasLifecycle)
	hasTaskEvidence := verifyTaskSamples(ctx, compiled, &report)
	verifyModelSamples(ctx, compiled, &report)
	verifyAgentSamples(ctx, compiled, &report)
	for _, direction := range DirectionCatalog() {
		if !compiled.Supports(direction) {
			continue
		}
		path := "/directions/" + string(direction)
		if !hasSample[direction] {
			report.Issues = append(report.Issues, verificationIssue(compiled, direction, path, IncompleteCoverage, "implemented direction requires a passing positive fixture", ""))
		}
		if isEventDirection(direction) && !hasSequence[direction] {
			report.Issues = append(report.Issues, verificationIssue(compiled, direction, path, IncompleteCoverage, "event direction requires a complete sequence fixture with terminal validation", ""))
		}
		for _, capability := range CapabilityCatalog() {
			if !compiled.mappings[direction].capabilities[capability] || !capabilityApplies(capability, direction) {
				continue
			}
			if !coverage[direction][capability] {
				issue := verificationIssue(compiled, direction, path+"/capabilities", IncompleteCoverage, "declared capability lacks passing semantic evidence", "")
				issue.Capability = capability
				report.Issues = append(report.Issues, issue)
			}
			if (direction == DecodeRequest || direction == EncodeRequest || (isEventDirection(direction) && compiled.capabilities[SessionsCapability])) && (capability == FunctionToolsCapability || capability == FreeTextToolsCapability) && !hasLifecycle[direction][capability] {
				issue := verificationIssue(compiled, direction, path+"/capabilities", IncompleteCoverage, "tool support requires a definition, associated call and result in a complete history fixture", "")
				issue.Capability = capability
				report.Issues = append(report.Issues, issue)
			}
		}
	}
	for _, capability := range CapabilityCatalog() {
		if !compiled.capabilities[capability] {
			continue
		}
		isCovered := capability == AsyncJobsCapability && hasTaskEvidence
		for _, direction := range DirectionCatalog() {
			isCovered = isCovered || coverage[direction][capability]
		}
		if isCovered {
			report.Covered = append(report.Covered, capability)
		} else {
			issue := verificationIssue(compiled, "", "/capabilities", IncompleteCoverage, "capability has no verified direction", "")
			issue.Capability = capability
			report.Issues = append(report.Issues, issue)
		}
	}
	report.Passed = IssuesError(report.Issues) == nil
	return report
}

func executeVerificationSample(ctx context.Context, compiled *Compiled, sample Sample) (verificationResult, error) {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	var result verificationResult
	var err error
	switch sample.Direction {
	case DecodeRequest:
		result.semantic, err = compiled.DecodeRequest(ctx, sample.Input.Bytes(), options)
	case EncodeRequest:
		request, failure := DecodeRequestContract(sample.Input.Bytes())
		if failure != nil {
			return result, failure
		}
		result.semantic = request
		var body []byte
		body, err = compiled.EncodeRequest(ctx, request, options)
		if err == nil {
			result.output, err = ParseValue(body)
		}
	case DecodeResponse:
		result.semantic, err = compiled.DecodeResponse(ctx, sample.Input.Bytes(), options)
	case EncodeResponse:
		var response Response
		if err := decodeContract(sample.Input.Bytes(), &response); err != nil {
			return result, err
		}
		result.semantic = &response
		var body []byte
		body, err = compiled.EncodeResponse(ctx, &response, options)
		if err == nil {
			result.output, err = ParseValue(body)
		}
	case DecodeEvent, EncodeEvent, DecodeClientEvent, EncodeUpstreamEvent:
		return executeEventFixture(ctx, compiled, sample)
	default:
		return result, fmt.Errorf("unsupported sample direction")
	}
	if err != nil {
		return result, err
	}
	if result.output.IsZero() {
		result.output, err = comparableSemantic(result.semantic)
	}
	return result, err
}

func executeEventFixture(ctx context.Context, compiled *Compiled, sample Sample) (verificationResult, error) {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	return executeEventFixtureWithState(ctx, compiled, sample, options)
}

func executeEventFixtureWithState(ctx context.Context, compiled *Compiled, sample Sample, options EvaluationContext) (verificationResult, error) {
	inputs := []Value{sample.Input}
	if sample.Sequence {
		var err error
		inputs, err = readArray(sample.Input)
		if err != nil {
			return verificationResult{}, err
		}
	}
	var events []Event
	var outputs []Value
	var frames []*EventFrame
	buffered := 0
	for _, input := range inputs {
		if isEventDecoder(sample.Direction) {
			frame, err := compiled.decodeFrame(ctx, sample.Direction, input, options)
			if err != nil {
				return verificationResult{}, err
			}
			frames = append(frames, frame)
			batch := frame.Events
			encoded, err := EncodeValue(batch)
			if err != nil {
				return verificationResult{}, err
			}
			buffered += len(encoded.raw)
			events = append(events, batch...)
		} else {
			var event Event
			if err := decodeContract(input.Bytes(), &event); err != nil {
				return verificationResult{}, err
			}
			wire, err := compiled.encodeFrames(ctx, eventEncoder(sample.Direction), event, options)
			if err != nil {
				return verificationResult{}, err
			}
			buffered += len(input.raw)
			for _, frame := range wire {
				buffered += len(frame.raw)
			}
			outputs = append(outputs, wire...)
			events = append(events, event)
		}
		if buffered > compiled.limits.BufferBytes {
			return verificationResult{}, streamIssue(LimitExceeded, "/samples", "event fixture exceeds replay buffer limit")
		}
	}
	if sample.Sequence {
		for _, event := range events {
			if isSessionControl(event.Type) {
				return verificationResult{}, streamIssue(IncompleteCoverage, "/samples", "session events require an interleaved sessionSamples trace")
			}
		}
		target := compiled.target(sample.Direction, options)
		target.Direction = EncodeEvent
		replay, err := NewEventReplay(target, compiled.limits)
		if err != nil {
			return verificationResult{}, err
		}
		for _, event := range events {
			if _, err := replay.Consume(event); err != nil {
				return verificationResult{}, err
			}
		}
		if err := replay.Finish(); err != nil {
			return verificationResult{}, err
		}
	}
	if sample.Sequence && !isEventDecoder(sample.Direction) {
		tail, err := compiled.finishEvents(ctx, eventEncoder(sample.Direction), options)
		if err != nil {
			return verificationResult{}, err
		}
		for _, frame := range tail {
			buffered += len(frame.raw)
		}
		if buffered > compiled.limits.BufferBytes {
			return verificationResult{}, streamIssue(LimitExceeded, "/samples", "event fixture exceeds replay buffer limit")
		}
		outputs = append(outputs, tail...)
	}
	result := verificationResult{semantic: events, frames: frames}
	var err error
	if isEventDecoder(sample.Direction) {
		result.output, err = comparableSemantic(events)
	} else if sample.Sequence || compiled.mappings[sample.Direction].frameBatch {
		result.output, err = EncodeValue(outputs)
	} else {
		result.output = outputs[0]
	}
	return result, err
}

func comparableExpected(sample Sample) (Value, error) {
	switch sample.Direction {
	case DecodeRequest:
		var request Request
		if err := decodeContract(sample.Expected.Bytes(), &request); err != nil {
			return Value{}, err
		}
		return comparableSemantic(&request)
	case DecodeResponse:
		var response Response
		if err := decodeContract(sample.Expected.Bytes(), &response); err != nil {
			return Value{}, err
		}
		return comparableSemantic(&response)
	case DecodeEvent, DecodeClientEvent:
		var events []Event
		if err := decodeContract(sample.Expected.Bytes(), &events); err != nil {
			return Value{}, err
		}
		return comparableSemantic(events)
	default:
		return sample.Expected, nil
	}
}

func capabilityApplies(capability Capability, direction Direction) bool {
	isRequest := direction == DecodeRequest || direction == EncodeRequest
	switch capability {
	case AsyncJobsCapability:
		return false // Task workflows have independent mapping fixtures.
	case UsageCapability:
		return !isRequest && direction != DecodeClientEvent && direction != EncodeUpstreamEvent
	case CacheKeysCapability, CacheRetentionCapability, CacheResourcesCapability, CacheBreakpointsCapability, CacheOptionsCapability, CachePrewarmCapability:
		return isRequest
	case SessionsCapability, RealtimeMediaCapability:
		return isEventDirection(direction)
	default:
		return true
	}
}

func verificationIssue(compiled *Compiled, direction Direction, path string, code IssueCode, reason, evidence string) ConversionIssue {
	return ConversionIssue{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "verify", Path: path, Reason: reason, Suggestion: "Correct the mapping or add representative assertions for the declared capability; do not remove required semantics to hide a failure.", Evidence: evidence}
}

func hasIssueCode(err error, expected IssueCode) bool {
	var conversion *ConversionError
	if !errors.As(err, &conversion) {
		return false
	}
	return slices.ContainsFunc(conversion.Issues, func(issue ConversionIssue) bool { return issue.Code == expected })
}

func sampleIssues(compiled *Compiled, sample Sample, path string, err error) []ConversionIssue {
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		issues := append([]ConversionIssue(nil), conversion.Issues...)
		for index := range issues {
			issues[index].Evidence = sample.ID
		}
		return issues
	}
	return []ConversionIssue{verificationIssue(compiled, sample.Direction, path, VerificationMismatch, err.Error(), sample.ID)}
}

func differencePath(expected, actual Value) string {
	if equalValues(expected, actual) {
		return ""
	}
	if expected.IsObject() && actual.IsObject() {
		first, _ := expected.ReadObject()
		second, _ := actual.ReadObject()
		for _, key := range sortedKeys(first) {
			if !equalValues(first[key], second[key]) {
				return "/" + escapePointer(key) + differencePath(first[key], second[key])
			}
		}
		for _, key := range sortedKeys(second) {
			if _, exists := first[key]; !exists {
				return "/" + escapePointer(key)
			}
		}
	}
	if valueType(expected) == ArrayType && valueType(actual) == ArrayType {
		first, _ := readArray(expected)
		second, _ := readArray(actual)
		for index := 0; index < len(first) && index < len(second); index++ {
			if !equalValues(first[index], second[index]) {
				return fmt.Sprintf("/%d", index) + differencePath(first[index], second[index])
			}
		}
	}
	return ""
}
