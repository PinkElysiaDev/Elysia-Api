package protocol

import (
	"bytes"
	"context"
	"errors"
	"slices"
)

// CombinationReport binds offline conversion evidence to both immutable
// revisions. It must be recalculated when either endpoint changes.
type CombinationReport struct {
	SourceSamplesHash string              `json:"sourceSamplesHash"`
	TargetSamplesHash string              `json:"targetSamplesHash"`
	ContextHash       string              `json:"contextHash,omitempty"`
	Fidelity          string              `json:"fidelity"`
	PolicyHash        string              `json:"policyHash,omitempty"`
	SourceHash        string              `json:"sourceHash"`
	TargetHash        string              `json:"targetHash"`
	CompilerVersion   string              `json:"compilerVersion"`
	Kind              VerificationKind    `json:"kind"`
	Passed            bool                `json:"passed"`
	Checks            []VerificationCheck `json:"checks"`
	Issues            []ConversionIssue   `json:"issues"`
	Capabilities      CapabilitySet       `json:"capabilities,omitempty"`
	BindingHash       string              `json:"bindingHash,omitempty"`
	IsRestricted      bool                `json:"restricted,omitempty"`
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
func VerifyBindingCombination(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet, policies ...*CompiledConversion) CombinationReport {
	if len(policies) > 0 {
		ctx = context.WithValue(ctx, conversionVerificationKey{}, policies[0])
	}
	return verifyCombination(ctx, ingress, upstream, capabilities)
}

func verifyCombination(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet) CombinationReport {
	report := CombinationReport{Fidelity: "preserved", SourceSamplesHash: ingress.SamplesHash(), TargetSamplesHash: upstream.SamplesHash(), SourceHash: ingress.hash, TargetHash: upstream.hash, CompilerVersion: CompilerVersion, Kind: OfflineVerification, Checks: []VerificationCheck{}, Issues: []ConversionIssue{}}
	conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion)
	if conversion == nil {
		conversion, _ = ResolveConversion(DefaultConversionPolicy(ingress, upstream))
	}
	ctx = context.WithValue(ctx, conversionVerificationKey{}, conversion)
	sink := &DiagnosticSink{}
	ctx = context.WithValue(ctx, conversionVerificationDiagnostics{}, sink)
	report.ContextHash = conversion.ContextHash()

	report.PolicyHash = ConversionHash(conversion)
	if capabilities != nil {
		report.BindingHash = CapabilityContractHash(capabilities)
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
		if recordEnvelopeRejection(conversion, err, check, &report) {
			continue
		}
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
		if recordEnvelopeRejection(conversion, err, check, &report) {
			continue
		}
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
			if recordEnvelopeRejection(conversion, err, VerificationCheck{SampleID: sample.ID, Direction: EncodeEvent}, &report) {
				continue
			}
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
	report.Issues = append(report.Issues, sink.Issues()...)
	report.Passed = IssuesError(report.Issues) == nil
	if !report.Passed {
		report.Fidelity = "rejected"
	} else {
		for _, issue := range sink.Issues() {
			if issue.Fidelity == "lossy_compatible" {
				report.Fidelity = "lossy_compatible"
			}
			if issue.Fidelity == "recoverable_wrapped" && report.Fidelity == "preserved" {
				report.Fidelity = "recoverable_wrapped"
			}
		}
	}
	return report
}

// Storage intent and required counters are value-dependent policy boundaries,
// not extra model capabilities. A rejected fixture supplies no positive coverage;
// independent passing request/response/stream fixtures are still mandatory.
func recordEnvelopeRejection(conversion *CompiledConversion, err error, check VerificationCheck, report *CombinationReport) bool {
	var failure *ConversionError
	if conversion == nil || !errors.As(err, &failure) || len(failure.Issues) == 0 {
		return false
	}
	for _, issue := range failure.Issues {
		allowed := false
		for _, rule := range conversion.Policy.Rules {
			if rule.Enabled && rule.ID == issue.RuleID && issue.Code == ConversionRejected && issue.PolicyHash == conversion.Hash {
				allowed = (rule.Action == "responses_storage" && issue.Path == "/store") ||
					(rule.Action == "anthropic_usage_envelope" && conversion.Policy.Mode == "strict" &&
						slices.Contains([]string{"/usage/input", "/usage/output", "/response/usage/input", "/response/usage/output"}, issue.Path))
			}
		}
		if !allowed {
			return false
		}
	}
	check.Rejected = true
	check.Reason = err.Error()
	report.Checks = append(report.Checks, check)
	return true
}

func inspectBindingSample(ctx context.Context, compiled *Compiled, sample Sample, allowed CapabilitySet, report *CombinationReport, direction Direction) (bool, []Capability) {
	result, err := executeVerificationSample(ctx, compiled, sample)
	if err != nil {
		return false, nil
	} // The normal replay reports the failure.
	observed := observeCapabilities(result.semantic).observed
	capabilities := []Capability{}
	for _, capability := range CapabilityCatalog() {
		if observed[capability] {
			capabilities = append(capabilities, capability)
		}
	}
	if allowed != nil && exceedsCapabilities(observed, allowed) {
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: direction, Skipped: true, Capabilities: capabilities, Reason: "sample exceeds the model binding's declared capabilities"})
		return true, nil
	}
	return false, capabilities
}

func verifyEventCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	result, err := executeEventFixture(ctx, upstream, sample)
	if err != nil {
		return err
	}
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: NewEvaluationState()}
	// This suite verifies transport usage independently of a particular client's
	// presentation preference.
	yes := true
	options.ClientOutput = &ClientOutput{IncludeUsage: &yes}
	conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion)
	projected := []Event{}
	route := ConversionContext{Source: upstream.Identity(), Target: ingress.Identity(), Transport: SSE}
	if conversion != nil {
		route = conversion.VerificationRoute(upstream.Identity(), ingress.Identity(), SSE)
	}
	route.Scope = options.Scope
	eventState := NewConversionEventState(conversion, route)
	convertedFrames := []*EventFrame{}
	for _, frame := range result.frames {
		original, _ := EncodeValue(frame.Events)
		events := []Event{}
		deliveryEvents := frame.Events
		if conversion.HasAnthropicEnvelope(ConversionEvent, route) {
			deliveryEvents = DeliveryFrameEvents(frame.Events)
		}
		for _, event := range deliveryEvents {
			queued, err := eventState.Push(event)
			if err != nil {
				return err
			}
			for _, next := range queued {
				if conversion != nil {
					value, encodeErr := EncodeValue(next)
					if encodeErr != nil {
						return encodeErr
					}
					proofRoute, proofErr := conversion.PreviewRecovery(ConversionEvent, value, route)
					if proofErr != nil {
						return proofErr
					}
					next, err = conversion.Event(ctx, next, proofRoute, verificationDiagnostics(ctx))
					if err != nil {
						return err
					}
				}
				events = append(events, next)
				projected = append(projected, next)
			}
		}
		if len(frame.Events) > 0 && len(events) == 0 {
			continue
		}
		updated, _ := EncodeValue(events)
		if eventState.Buffered || !bytes.Equal(original.Bytes(), updated.Bytes()) {
			frame = &EventFrame{Events: events}
		}
		convertedFrames = append(convertedFrames, frame)
	}
	tailEvents, err := eventState.Drain()
	if err != nil {
		return err
	}
	for _, event := range tailEvents {
		event, err = conversion.Event(ctx, event, route, verificationDiagnostics(ctx))
		if err != nil {
			return err
		}
		projected = append(projected, event)
		convertedFrames = append(convertedFrames, &EventFrame{Events: []Event{event}})
	}
	result.semantic = projected
	var frames []Value
	var decoded []Event
	for _, frame := range convertedFrames {
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
	finalValidation, err := ingress.NewWireStreamValidation(options.Scope)
	if err != nil {
		return err
	}
	for _, wire := range frames {
		if conversion != nil && conversion.HasPhase(ConversionWire) {
			wire, err = conversion.ApplyValue(ctx, ConversionWire, wire, route, verificationDiagnostics(ctx))
			if err != nil {
				return err
			}
		}
		if err := finalValidation.Consume(ctx, wire); err != nil {
			return err
		}
		if !ingress.Supports(DecodeEvent) {
			continue
		}
		frame, err := ingress.DecodeFrame(ctx, wire, options)
		if err != nil {
			return err
		}
		decoded = append(decoded, frame.Events...)
	}
	if err := finalValidation.Finish(); err != nil {
		return err
	}
	if !ingress.Supports(DecodeEvent) {
		return nil
	}
	var comparison error
	if hasSameWire(ingress, upstream) && ingress.native.Preserve && upstream.native.Preserve && !eventState.Buffered {
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
	if conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion); conversion != nil {
		route := conversion.VerificationRoute(ingress.Identity(), upstream.Identity(), HTTPJSON)
		route.Scope = options.Scope
		request, err = conversion.Request(ctx, request, route, verificationDiagnostics(ctx))
		if err != nil {
			return err
		}
	}
	wire, err := upstream.EncodeRequest(ctx, request, options)
	if err != nil {
		return err
	}
	if v, e := ParseValue(wire); e != nil {
		return e
	} else if e = upstream.ValidateWireOutput(EncodeRequest, v); e != nil {
		return e
	}
	for _, name := range sortedKeys(upstream.operations) {
		operation := upstream.operations[name]
		if operation.Kind == "generate" || operation.Kind == "submit" {
			if err := upstream.CheckOperationInput(operation, wire); err != nil {
				return err
			}
		}
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
	if conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion); conversion != nil {
		route := conversion.VerificationRoute(upstream.Identity(), ingress.Identity(), HTTPJSON)
		route.Scope = options.Scope
		value, encodeErr := EncodeValue(response)
		if encodeErr != nil {
			return encodeErr
		}
		route, err = conversion.PreviewRecovery(ConversionResponse, value, route)
		if err != nil {
			return err
		}
		response, err = conversion.Response(ctx, response, route, verificationDiagnostics(ctx))
		if err != nil {
			return err
		}
	}
	wire, err := ingress.EncodeResponse(ctx, response, options)
	if err != nil {
		return err
	}
	wireValue, _ := ParseValue(wire)
	if conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion); conversion != nil && conversion.HasPhase(ConversionWire) {
		route := conversion.VerificationRoute(upstream.Identity(), ingress.Identity(), HTTPJSON)
		route.Scope = options.Scope
		wireValue, err = conversion.ApplyValue(ctx, ConversionWire, wireValue, route, verificationDiagnostics(ctx))
		if err != nil {
			return err
		}
		wire = wireValue.Bytes()
	}
	if err := ingress.ValidateWireOutput(EncodeResponse, wireValue); err != nil {
		return err
	}
	if !ingress.Supports(DecodeResponse) {
		return nil
	}
	decoded, err := ingress.DecodeResponse(ctx, wire, options)
	if err != nil {
		return err
	}
	return compareRoundTrip(ingress, sample, equivalentResponse(response, ingress.identity.Family), equivalentResponse(decoded, ingress.identity.Family))
}

type conversionVerificationKey struct{}

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

type conversionVerificationDiagnostics struct{}

func verificationDiagnostics(ctx context.Context) *DiagnosticSink {
	sink, _ := ctx.Value(conversionVerificationDiagnostics{}).(*DiagnosticSink)
	return sink
}
