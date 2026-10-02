package protocol

import (
	"context"
	"slices"
)

// CombinationReport binds offline conversion evidence to both immutable
// revisions. It must be recalculated when either endpoint changes.
type CombinationReport struct {
	SourceHash      string              `json:"sourceHash"`
	TargetHash      string              `json:"targetHash"`
	CompilerVersion string              `json:"compilerVersion"`
	Kind            VerificationKind    `json:"kind"`
	Passed          bool                `json:"passed"`
	Checks          []VerificationCheck `json:"checks"`
	Issues          []ConversionIssue   `json:"issues"`
	Capabilities    CapabilitySet       `json:"capabilities,omitempty"`
}

// VerifyCombination replays ingress request and upstream response fixtures
// through both adapters. Incompatible capabilities produce explicit diagnostics.
// A partial adapter needs its own positive expected-wire fixtures; independent
// semantic roundtrip checks are added whenever its inverse direction exists.
func VerifyCombination(ctx context.Context, ingress, upstream *Compiled) CombinationReport {
	return verifyCombination(ctx, ingress, upstream, nil)
}

// VerifyBindingCombination verifies the explicitly restricted model contract.
// Samples outside that contract are reported as skipped; missing evidence for
// any promised capability still blocks the binding.
func VerifyBindingCombination(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet) CombinationReport {
	return verifyCombination(ctx, ingress, upstream, capabilities)
}

func verifyCombination(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet) CombinationReport {
	report := CombinationReport{SourceHash: ingress.hash, TargetHash: upstream.hash, CompilerVersion: CompilerVersion, Kind: OfflineVerification, Checks: []VerificationCheck{}, Issues: []ConversionIssue{}}
	if capabilities != nil {
		report.Capabilities = CapabilitySet{}
		for capability, supported := range capabilities {
			report.Capabilities[capability] = supported
		}
	}
	hasSession := len(sessionOperations(ingress)) > 0 && len(sessionOperations(upstream)) > 0 && (capabilities == nil || capabilities[SessionsCapability])
	hasHTTP := hasHTTPGeneration(ingress) && hasHTTPGeneration(upstream)
	required := []struct {
		compiled  *Compiled
		direction Direction
	}{{ingress, DecodeRequest}, {upstream, EncodeRequest}}
	if hasHTTP {
		required = append(required, struct {
			compiled  *Compiled
			direction Direction
		}{ingress, EncodeResponse}, struct {
			compiled  *Compiled
			direction Direction
		}{upstream, DecodeResponse})
	}
	for _, binding := range required {
		if !binding.compiled.Supports(binding.direction) {
			report.Issues = append(report.Issues, verificationIssue(binding.compiled, binding.direction, "/directions", UnsupportedCapability, "composition requires this adapter direction", ""))
		}
	}
	if !hasHTTP && !hasSession {
		report.Issues = append(report.Issues, verificationIssue(ingress, "", "/operations", UnsupportedCapability, "protocols have no shared generation or session transport", ""))
	}
	if len(report.Issues) > 0 {
		return report
	}
	for _, sample := range ingress.Definition().Samples {
		if sample.Direction != DecodeRequest || sample.ExpectedIssue != "" {
			continue
		}
		check := VerificationCheck{SampleID: sample.ID, Direction: EncodeRequest}
		isSkipped, observed := inspectBindingSample(ctx, ingress, sample, capabilities, &report, EncodeRequest)
		if isSkipped {
			continue
		}
		err := verifyRequestCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
		check.Capabilities = observed
		if err != nil {
			report.Issues = append(report.Issues, sampleIssues(upstream, sample, "/combination/request", err)...)
		}
		report.Checks = append(report.Checks, check)
	}
	for _, sample := range upstream.Definition().Samples {
		if !hasHTTP || sample.Direction != DecodeResponse || sample.ExpectedIssue != "" {
			continue
		}
		check := VerificationCheck{SampleID: sample.ID, Direction: EncodeResponse}
		isSkipped, observed := inspectBindingSample(ctx, upstream, sample, capabilities, &report, EncodeResponse)
		if isSkipped {
			continue
		}
		err := verifyResponseCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
		check.Capabilities = observed
		if err != nil {
			report.Issues = append(report.Issues, sampleIssues(ingress, sample, "/combination/response", err)...)
		}
		report.Checks = append(report.Checks, check)
	}
	if hasHTTP && ((len(sessionOperations(ingress)) == 0 && len(sessionOperations(upstream)) == 0) || (hasHTTPStream(upstream) && hasHTTPStream(ingress))) && upstream.Supports(DecodeEvent) && ingress.Supports(EncodeEvent) {
		hasSequence := false
		for _, sample := range upstream.Definition().Samples {
			if sample.Direction != DecodeEvent || !sample.Sequence || sample.ExpectedIssue != "" {
				continue
			}
			isSkipped, observed := inspectBindingSample(ctx, upstream, sample, capabilities, &report, EncodeEvent)
			if isSkipped {
				continue
			}
			err := verifyEventCombination(ctx, ingress, upstream, sample)
			if err != nil && !hasSameWire(ingress, upstream) && slices.Contains(observed, NativeExtensionsCapability) && hasIssueCode(err, UnsupportedNative) {
				report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: EncodeEvent, Passed: true, Reason: "foreign native event extensions were explicitly rejected"})
				continue
			}
			report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: EncodeEvent, Passed: err == nil, Capabilities: observed})
			if err != nil {
				report.Issues = append(report.Issues, sampleIssues(ingress, sample, "/combination/events", err)...)
			} else {
				hasSequence = true
			}
		}
		if !hasSequence {
			report.Issues = append(report.Issues, verificationIssue(ingress, EncodeEvent, "/combination/events", IncompleteCoverage, "event composition requires a complete sequence fixture", ""))
		}
	}
	hasSessionEvidence := false
	if hasSession {
		hasSessionEvidence = verifySessionCombination(ctx, ingress, upstream, &report)
	}
	verifyTaskCombination(ctx, ingress, upstream, capabilities, &report)
	if capabilities == nil || capabilities[NativeExtensionsCapability] {
		verifyCombinationNative(ctx, ingress, upstream, &report)
	}
	hasRequest, hasResponse := false, false
	for _, check := range report.Checks {
		hasRequest = hasRequest || (check.Passed && check.Direction == EncodeRequest)
		hasResponse = hasResponse || (check.Passed && check.Direction == EncodeResponse)
	}
	if !hasRequest || (hasHTTP && !hasResponse) || (hasSession && !hasSessionEvidence) {
		report.Issues = append(report.Issues, verificationIssue(ingress, "", "/combination", IncompleteCoverage, "composition requires passing request and response fixtures", ""))
	}
	for _, capability := range sortedKeys(capabilities) {
		if !capabilities[capability] {
			continue
		}
		if capability == NativeExtensionsCapability && !hasSameWire(ingress, upstream) {
			continue
		}
		hasEvidence := false
		for _, check := range report.Checks {
			if !check.Passed {
				continue
			}
			for _, observed := range check.Capabilities {
				hasEvidence = hasEvidence || observed == capability
			}
		}
		if !hasEvidence {
			report.Issues = append(report.Issues, verificationIssue(upstream, "", "/binding/capabilities/"+string(capability), IncompleteCoverage, "model binding has no successful paired fixture for this capability", ""))
		}
	}
	report.Passed = IssuesError(report.Issues) == nil
	return report
}

func inspectBindingSample(ctx context.Context, compiled *Compiled, sample Sample, allowed CapabilitySet, report *CombinationReport, direction Direction) (bool, []Capability) {
	result, err := executeVerificationSample(ctx, compiled, sample)
	if err != nil {
		return false, nil
	} // The normal replay reports the failure.
	observed := observeCapabilities(result.semantic).observed
	if allowed != nil && exceedsCapabilities(observed, allowed) {
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: direction, Skipped: true, Reason: "sample exceeds the model binding's declared capabilities"})
		return true, nil
	}
	capabilities := []Capability{}
	for _, capability := range CapabilityCatalog() {
		if observed[capability] {
			capabilities = append(capabilities, capability)
		}
	}
	return false, capabilities
}

func verifyEventCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	result, err := executeEventFixture(ctx, upstream, sample)
	if err != nil {
		return err
	}
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	var frames []Value
	var decoded []Event
	for _, frame := range result.frames {
		wire, err := ingress.EncodeFrame(ctx, frame, options)
		if err != nil {
			return err
		}
		frames = append(frames, wire...)
	}
	tail, err := ingress.FinishEvents(ctx, options)
	if err != nil {
		return err
	}
	frames = append(frames, tail...)
	if !ingress.Supports(DecodeEvent) {
		return nil
	}
	for _, wire := range frames {
		frame, err := ingress.DecodeFrame(ctx, wire, options)
		if err != nil {
			return err
		}
		decoded = append(decoded, frame.Events...)
	}
	var comparison error
	if hasSameWire(ingress, upstream) && ingress.native.Preserve && upstream.native.Preserve {
		comparison = compareRoundTrip(ingress, sample, result.semantic, decoded)
	} else {
		comparison = compareEventSequence(ingress, sample, result.semantic.([]Event), decoded)
	}
	if comparison != nil {
		return comparison
	}
	target := ingress.target(EncodeEvent, options)
	replay, err := NewEventReplay(target, ingress.limits)
	if err != nil {
		return err
	}
	for _, event := range decoded {
		if _, err := replay.Consume(event); err != nil {
			return err
		}
	}
	return replay.Finish()
}

func verifyRequestCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	request, err := ingress.DecodeRequest(ctx, sample.Input.Bytes(), options)
	if err != nil {
		return err
	}
	wire, err := upstream.EncodeRequest(ctx, request, options)
	if err != nil {
		return err
	}
	if !upstream.Supports(DecodeRequest) {
		return nil
	}
	decoded, err := upstream.DecodeRequest(ctx, wire, options)
	if err != nil {
		return err
	}
	return compareRoundTrip(upstream, sample, equivalentRequest(request), equivalentRequest(decoded))
}

func verifyResponseCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	response, err := upstream.DecodeResponse(ctx, sample.Input.Bytes(), options)
	if err != nil {
		return err
	}
	wire, err := ingress.EncodeResponse(ctx, response, options)
	if err != nil {
		return err
	}
	if !ingress.Supports(DecodeResponse) {
		return nil
	}
	decoded, err := ingress.DecodeResponse(ctx, wire, options)
	if err != nil {
		return err
	}
	return compareRoundTrip(ingress, sample, equivalentResponse(response), equivalentResponse(decoded))
}

func hasHTTPGeneration(compiled *Compiled) bool {
	for _, operation := range compiled.operations {
		if (operation.Kind == "generate" || operation.Kind == "submit") && operation.Transport != WebSocket {
			return true
		}
	}
	return false
}
func hasHTTPStream(compiled *Compiled) bool {
	for _, operation := range compiled.operations {
		if operation.Transport == SSE || operation.Transport == NDJSON {
			return true
		}
	}
	return false
}
