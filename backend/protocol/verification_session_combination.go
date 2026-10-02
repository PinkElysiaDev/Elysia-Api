package protocol

import "context"

func sessionOperations(compiled *Compiled) map[string]Operation {
	operations := map[string]Operation{}
	for name, operation := range compiled.operations {
		if operation.Transport == WebSocket {
			operations[name] = operation
		}
	}
	return operations
}

func verifySessionCombination(ctx context.Context, ingress, upstream *Compiled, report *CombinationReport) bool {
	clients, servers := sessionOperations(ingress), sessionOperations(upstream)
	if len(clients) == 0 || len(servers) == 0 {
		return false
	}
	for _, entry := range []struct {
		compiled   *Compiled
		directions []Direction
	}{{ingress, []Direction{DecodeClientEvent, EncodeEvent}}, {upstream, []Direction{EncodeUpstreamEvent, DecodeEvent}}} {
		for _, direction := range entry.directions {
			if !entry.compiled.Supports(direction) {
				report.Issues = append(report.Issues, verificationIssue(entry.compiled, direction, "/combination/session", UnsupportedCapability, "session combination requires this direction", ""))
				return false
			}
		}
	}
	hasEvidence := false
	for clientName, client := range clients {
		for serverName, server := range servers {
			if err := CheckSessionCompatibility(client, server); err != nil {
				report.Issues = append(report.Issues, sampleIssues(ingress, Sample{ID: clientName + "/" + serverName}, "/combination/session", err)...)
				continue
			}
			for _, pair := range []struct {
				source, target  *Compiled
				operation       string
				targetOperation Operation
			}{{ingress, upstream, clientName, server}, {upstream, ingress, serverName, client}} {
				hasTrace := false
				for _, sample := range pair.source.Definition().SessionSamples {
					if sample.Operation != pair.operation || sample.ExpectedIssue != "" {
						continue
					}
					evidence, err := executeSessionSample(ctx, pair.source, sample)
					if err != nil {
						report.Issues = append(report.Issues, sampleIssues(pair.source, Sample{ID: sample.ID}, "/combination/session", err)...)
						continue
					}
					observed := observeCapabilities(evidence.events).observed
					if report.Capabilities != nil && exceedsCapabilities(observed, report.Capabilities) {
						report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Skipped: true, Reason: "session trace exceeds the binding capabilities"})
						continue
					}
					err = replaySessionCombination(ctx, pair.target, pair.targetOperation, sample, evidence)
					if err != nil {
						report.Issues = append(report.Issues, sampleIssues(pair.target, Sample{ID: sample.ID}, "/combination/session", err)...)
					} else {
						hasTrace = true
						hasEvidence = true
					}
					for direction, capabilities := range evidence.coverage {
						check := VerificationCheck{SampleID: sample.ID, Direction: eventEncoder(direction), Passed: err == nil}
						for _, capability := range CapabilityCatalog() {
							if capabilities[capability] {
								check.Capabilities = append(check.Capabilities, capability)
							}
						}
						report.Checks = append(report.Checks, check)
					}
				}
				if !hasTrace {
					report.Issues = append(report.Issues, verificationIssue(pair.source, "", "/combination/session", IncompleteCoverage, "each session endpoint requires a passing trace through the other endpoint", pair.operation))
				}
			}
		}
	}
	return hasEvidence
}

func replaySessionCombination(ctx context.Context, target *Compiled, operation Operation, sample SessionSample, evidence sessionEvidence) error {
	options := EvaluationContext{Scope: sample.Scope, Values: sample.Context}
	replay, err := NewSessionReplay(sessionLaneTarget(target, ClientEvent, options), sessionLaneTarget(target, UpstreamEvent, options), target.limits, SessionPolicy{Model: sample.Model, CanGenerateAutomatically: operation.Session.CanGenerateAutomatically})
	if err != nil {
		return err
	}
	for _, entry := range evidence.trace {
		direction := eventEncoder(entry.direction)
		decoded := []Event{entry.event}
		if target.Supports(direction) {
			wire, err := target.encodeEvent(ctx, direction, entry.event, options)
			if err != nil {
				return err
			}
			if target.Supports(eventDecoder(direction)) {
				decoded, err = target.decodeEvents(ctx, eventDecoder(direction), wire, options)
				if err != nil {
					return err
				}
				if err := compareRoundTrip(target, Sample{ID: sample.ID, Direction: direction}, []Event{entry.event}, decoded); err != nil {
					return err
				}
			}
		}
		for _, event := range decoded {
			if _, err := replay.Consume(eventOrigin(direction), event); err != nil {
				return err
			}
		}
	}
	return replay.Finish()
}

func exceedsCapabilities(observed, allowed CapabilitySet) bool {
	for capability, supported := range observed {
		if supported && !allowed[capability] {
			return true
		}
	}
	return false
}
