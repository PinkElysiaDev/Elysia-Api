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
}

// VerifyCombination replays ingress request and upstream response fixtures
// through both adapters. Incompatible capabilities produce explicit diagnostics.
// A partial adapter needs its own positive expected-wire fixtures; independent
// semantic roundtrip checks are added whenever its inverse direction exists.
func VerifyCombination(ctx context.Context, ingress, upstream *Compiled) CombinationReport {
	report := CombinationReport{SourceHash: ingress.hash, TargetHash: upstream.hash, CompilerVersion: CompilerVersion, Kind: OfflineVerification, Checks: []VerificationCheck{}, Issues: []ConversionIssue{}}
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
		err := verifyRequestCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
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
		err := verifyResponseCombination(ctx, ingress, upstream, sample)
		check.Passed = err == nil
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
			err := verifyEventCombination(ctx, ingress, upstream, sample)
			report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: EncodeEvent, Passed: err == nil})
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
	report.Passed = IssuesError(report.Issues) == nil
	return report
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
