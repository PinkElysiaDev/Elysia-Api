package protocol

import (
	"context"
	"fmt"
)

type sessionEvidence struct {
	checks   []VerificationCheck
	coverage map[Direction]CapabilitySet
	events   []Event
	trace    []sessionTraceEvent
}

type sessionTraceEvent struct {
	direction Direction
	event     Event
}

func executeSessionSample(ctx context.Context, compiled *Compiled, sample SessionSample) (sessionEvidence, error) {
	result := sessionEvidence{coverage: map[Direction]CapabilitySet{}}
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
	operation := compiled.operations[sample.Operation]
	replay, err := NewSessionReplay(sessionLaneTarget(compiled, ClientEvent, options), sessionLaneTarget(compiled, UpstreamEvent, options), compiled.limits, SessionPolicy{Model: sample.Model, CanGenerateAutomatically: operation.Session.CanGenerateAutomatically})
	if err != nil {
		return result, err
	}
	buffered := 0
	for index, step := range sample.Steps {
		fixture := Sample{ID: fmt.Sprintf("%s/steps/%d", sample.ID, index), Direction: step.Direction, Input: step.Input, Expected: step.Expected, Context: sample.Context, Scope: sample.Scope}
		value, err := executeEventFixture(ctx, compiled, fixture)
		if err != nil {
			return result, err
		}
		expected, err := comparableExpected(fixture)
		if err != nil {
			return result, err
		}
		if !equalValues(expected, value.output) {
			return result, IssuesError([]ConversionIssue{verificationIssue(compiled, step.Direction, fmt.Sprintf("/sessionSamples/%s/steps/%d/expected", sample.ID, index)+differencePath(expected, value.output), VerificationMismatch, "session step differs from its expected output", fixture.ID)})
		}
		if err := verifyEventRoundTrip(ctx, compiled, fixture, value); err != nil {
			return result, err
		}
		events := value.semantic.([]Event)
		for _, event := range events {
			if _, err := replay.Consume(eventOrigin(step.Direction), event); err != nil {
				return result, err
			}
			result.trace = append(result.trace, sessionTraceEvent{direction: step.Direction, event: event})
		}
		encoded, err := EncodeValue(events)
		if err != nil {
			return result, err
		}
		buffered += len(encoded.raw) + len(value.output.raw)
		if buffered > compiled.limits.BufferBytes {
			return result, streamIssue(LimitExceeded, "/sessionSamples", "session replay exceeds its aggregate buffer limit")
		}
		result.events = append(result.events, events...)
		if result.coverage[step.Direction] == nil {
			result.coverage[step.Direction] = CapabilitySet{}
		}
		check := VerificationCheck{SampleID: fixture.ID, Direction: step.Direction, Passed: true}
		observed := observeCapabilities(events).observed
		for _, capability := range CapabilityCatalog() {
			if observed[capability] {
				result.coverage[step.Direction][capability] = true
				check.Capabilities = append(check.Capabilities, capability)
			}
		}
		result.checks = append(result.checks, check)
	}
	return result, replay.Finish()
}

func eventOrigin(direction Direction) EventOrigin {
	if direction == DecodeClientEvent || direction == EncodeUpstreamEvent {
		return ClientEvent
	}
	return UpstreamEvent
}

func sessionLaneTarget(compiled *Compiled, origin EventOrigin, options EvaluationContext) Target {
	directions := []Direction{DecodeEvent, EncodeEvent}
	if origin == ClientEvent {
		directions = []Direction{DecodeClientEvent, EncodeUpstreamEvent}
	}
	target := compiled.target(directions[1], options)
	target.Capabilities = CapabilitySet{}
	for _, direction := range directions {
		for capability, supported := range compiled.Capabilities(direction) {
			target.Capabilities[capability] = target.Capabilities[capability] || supported
		}
	}
	return target
}

func verifySessionSamples(ctx context.Context, compiled *Compiled, report *VerificationReport, coverage map[Direction]CapabilitySet, hasSample, hasSequence map[Direction]bool, hasLifecycle map[Direction]map[Capability]bool) {
	hasOperation := map[string]bool{}
	for index, sample := range compiled.Definition().SessionSamples {
		evidence, err := executeSessionSample(ctx, compiled, sample)
		path := fmt.Sprintf("/sessionSamples/%d", index)
		check := VerificationCheck{SampleID: sample.ID, Passed: err == nil}
		if sample.ExpectedIssue != "" {
			check.Passed = hasIssueCode(err, sample.ExpectedIssue)
			report.Checks = append(report.Checks, check)
			if !check.Passed {
				report.Issues = append(report.Issues, verificationIssue(compiled, "", path, VerificationMismatch, "session sample did not produce its expected diagnostic", sample.ID))
			}
			continue
		}
		if err != nil {
			report.Checks = append(report.Checks, check)
			report.Issues = append(report.Issues, sampleIssues(compiled, Sample{ID: sample.ID}, path, err)...)
			continue
		}
		report.Checks = append(report.Checks, evidence.checks...)
		hasOperation[sample.Operation] = true
		lifecycle := observeSessionLifecycle(evidence.events)
		for direction, observed := range evidence.coverage {
			hasSample[direction], hasSequence[direction] = true, true
			for capability, supported := range observed {
				coverage[direction][capability] = coverage[direction][capability] || supported
			}
			for capability, supported := range lifecycle {
				hasLifecycle[direction][capability] = hasLifecycle[direction][capability] || supported
			}
		}
	}
	for name, operation := range compiled.operations {
		if operation.Transport == WebSocket && !hasOperation[name] {
			report.Issues = append(report.Issues, verificationIssue(compiled, "", "/operations/"+name, IncompleteCoverage, "session operation requires a passing interleaved session sample", ""))
		}
	}
}

func observeSessionLifecycle(events []Event) CapabilitySet {
	definitions := map[string]ToolKind{}
	calls := map[string]InputKind{}
	covered := CapabilitySet{}
	for _, event := range events {
		if event.Request != nil {
			for _, tool := range event.Request.Tools {
				definitions[tool.Name.raw] = tool.Kind
			}
		}
		if event.Item == nil {
			continue
		}
		item := event.Item
		if item.Kind == ToolCallNode && item.Input != nil {
			kind := definitions[item.Name.raw]
			if (kind == FunctionTool && item.Input.Kind == JSONInput) || (kind == FreeTextTool && item.Input.Kind == TextInput) {
				calls[item.CallID.raw] = item.Input.Kind
			}
		}
		if event.Type == ToolResultSubmitted {
			switch calls[item.CallID.raw] {
			case JSONInput:
				covered[FunctionToolsCapability] = true
			case TextInput:
				covered[FreeTextToolsCapability] = true
			}
		}
	}
	return covered
}
