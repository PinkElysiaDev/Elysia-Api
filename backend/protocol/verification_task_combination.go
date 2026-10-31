package protocol

import (
	"context"
	"fmt"
)

func verifyTaskCombination(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet, report *CombinationReport) {
	if len(upstream.taskMappings) == 0 || (capabilities != nil && !capabilities[AsyncJobsCapability]) {
		return
	}
	proof := VerificationReport{}
	if !verifyTaskSamples(ctx, upstream, &proof) || len(proof.Issues) > 0 {
		report.Issues = append(report.Issues, proof.Issues...)
		return
	}
	options := newVerificationContext(Scope{}, nil)
	defer options.State.Release()
	for _, sample := range upstream.verificationTaskSamples() {
		if sample.Kind != "decode" {
			continue
		}
		update, err := upstream.DecodeTaskUpdate(ctx, sample.Operation, sample.Purpose, sample.Input, options)
		if err != nil {
			continue
		} // Own-flow proof already reports decoding errors.
		isPassed := true
		for name := range ingress.taskMappings {
			wire, err := ingress.EncodeTaskReceipt(ctx, name, sample.Purpose, update, options)
			if err == nil {
				var decoded JobUpdate
				decoded, err = ingress.DecodeTaskUpdate(ctx, name, sample.Purpose, wire, options)
				if err == nil {
					before, _ := EncodeValue(update)
					after, _ := EncodeValue(decoded)
					if !equalValues(before, after) {
						err = fmt.Errorf("paired task receipt loses identity, state or usage")
					}
				}
			}
			if err != nil {
				isPassed = false
				report.Issues = append(report.Issues, verificationIssue(ingress, "", "/combination/tasks", VerificationMismatch, err.Error(), sample.ID))
			}
		}
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Passed: isPassed, Capabilities: []Capability{AsyncJobsCapability}})
	}
}
