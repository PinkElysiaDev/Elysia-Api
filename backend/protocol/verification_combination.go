package protocol

import "context"

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
	for _, binding := range []struct {
		compiled  *Compiled
		direction Direction
	}{{ingress, DecodeRequest}, {ingress, EncodeResponse}, {upstream, EncodeRequest}, {upstream, DecodeResponse}} {
		if !binding.compiled.Supports(binding.direction) {
			report.Issues = append(report.Issues, verificationIssue(binding.compiled, binding.direction, "/directions", UnsupportedCapability, "composition requires this adapter direction", ""))
		}
	}
	if len(report.Issues) > 0 {
		return report
	}
	for _, sample := range ingress.Definition().Samples {
		if sample.Direction != DecodeRequest || sample.ExpectedIssue != "" {
			continue
		}
		check := VerificationCheck{SampleID: sample.ID, Direction: EncodeRequest}
		if skipBindingSample(ctx, ingress, sample, capabilities, &report, EncodeRequest) {
			continue
		}
		err := verifyRequestCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
		check.Capabilities = sample.Capabilities
		if err != nil {
			report.Issues = append(report.Issues, sampleIssues(upstream, sample, "/combination/request", err)...)
		}
		report.Checks = append(report.Checks, check)
	}
	for _, sample := range upstream.Definition().Samples {
		if sample.Direction != DecodeResponse || sample.ExpectedIssue != "" {
			continue
		}
		check := VerificationCheck{SampleID: sample.ID, Direction: EncodeResponse}
		if skipBindingSample(ctx, upstream, sample, capabilities, &report, EncodeResponse) {
			continue
		}
		err := verifyResponseCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
		check.Capabilities = sample.Capabilities
		if err != nil {
			report.Issues = append(report.Issues, sampleIssues(ingress, sample, "/combination/response", err)...)
		}
		report.Checks = append(report.Checks, check)
	}
	if upstream.Supports(DecodeEvent) && ingress.Supports(EncodeEvent) {
		hasSequence := false
		for _, sample := range upstream.Definition().Samples {
			if sample.Direction != DecodeEvent || !sample.Sequence || sample.ExpectedIssue != "" {
				continue
			}
			if skipBindingSample(ctx, upstream, sample, capabilities, &report, EncodeEvent) {
				continue
			}
			err := verifyEventCombination(ctx, ingress, upstream, sample)
			report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: EncodeEvent, Passed: err == nil, Capabilities: sample.Capabilities})
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
	hasRequest, hasResponse := false, false
	for _, check := range report.Checks {
		hasRequest = hasRequest || (check.Passed && check.Direction == EncodeRequest)
		hasResponse = hasResponse || (check.Passed && check.Direction == EncodeResponse)
	}
	if !hasRequest || !hasResponse {
		report.Issues = append(report.Issues, verificationIssue(ingress, "", "/combination", IncompleteCoverage, "composition requires passing request and response fixtures", ""))
	}
	for _, capability := range sortedKeys(capabilities) {
		if !capabilities[capability] {
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

func skipBindingSample(ctx context.Context, compiled *Compiled, sample Sample, allowed CapabilitySet, report *CombinationReport, direction Direction) bool {
	if allowed == nil {
		return false
	}
	result, err := executeVerificationSample(ctx, compiled, sample)
	if err != nil {
		return false
	} // The normal replay reports the failure.
	for capability := range observeCapabilities(result.semantic).observed {
		if allowed[capability] {
			continue
		}
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: direction, Skipped: true, Reason: "sample exceeds the model binding's declared capabilities"})
		return true
	}
	return false
}

func verifyEventCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	result, err := executeEventFixture(ctx, upstream, sample)
	if err != nil {
		return err
	}
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
	var decoded []Event
	for _, event := range result.semantic.([]Event) {
		wire, err := ingress.EncodeEvent(ctx, event, options)
		if err != nil {
			return err
		}
		if ingress.Supports(DecodeEvent) {
			batch, err := ingress.DecodeEvents(ctx, wire, options)
			if err != nil {
				return err
			}
			decoded = append(decoded, batch...)
		}
	}
	if !ingress.Supports(DecodeEvent) {
		return nil
	}
	if err := compareRoundTrip(ingress, sample, result.semantic, decoded); err != nil {
		return err
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
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
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
	return compareRoundTrip(upstream, sample, request, decoded)
}

func verifyResponseCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
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
	return compareRoundTrip(ingress, sample, response, decoded)
}
