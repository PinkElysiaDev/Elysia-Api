package protocol

import (
	"context"
	"fmt"
)

func executeTaskSample(ctx context.Context, compiled *Compiled, sample TaskSample) (Value, error) {
	switch sample.Kind {
	case "decode":
		update, err := compiled.DecodeTaskUpdate(ctx, sample.Operation, sample.Purpose, sample.Input)
		if err != nil {
			return Value{}, err
		}
		return EncodeValue(update)
	case "encode":
		var receipt JobUpdate
		if err := decodeContract(sample.Input.Bytes(), &receipt); err != nil {
			return Value{}, err
		}
		return compiled.EncodeTaskReceipt(ctx, sample.Operation, sample.Purpose, receipt)
	case "control":
		var input struct {
			ID string `json:"id"`
		}
		if err := decodeContract(sample.Input.Bytes(), &input); err != nil {
			return Value{}, err
		}
		control, err := compiled.EncodeTaskControl(ctx, sample.Operation, sample.Purpose, input.ID)
		if err != nil {
			return Value{}, err
		}
		return EncodeValue(control)
	default:
		return Value{}, fmt.Errorf("invalid task sample kind")
	}
}

func verifyTaskSamples(ctx context.Context, compiled *Compiled, report *VerificationReport) bool {
	coverage := map[string]map[string]bool{}
	for name := range compiled.taskMappings {
		coverage[name] = map[string]bool{}
	}
	for _, sample := range compiled.Definition().TaskSamples {
		output, err := executeTaskSample(ctx, compiled, sample)
		if err == nil && !equalValues(output, sample.Expected) {
			err = fmt.Errorf("task sample differs from expected output")
		}
		// Encoding a local ID/state must remain decodable. This prevents a
		// constant receipt from passing fixtures while hiding the actual job ID.
		if err == nil && sample.Kind == "encode" {
			decoded, issues := compiled.ConvertTask(ctx, sample.Operation, "decode", sample.Purpose, output)
			if issues != nil {
				err = issues
			} else if !equalValues(decoded, sample.Input) {
				err = fmt.Errorf("task receipt roundtrip loses identity, state or counters")
			}
		}
		check := VerificationCheck{SampleID: sample.ID, Passed: err == nil}
		if err != nil {
			report.Issues = append(report.Issues, verificationIssue(compiled, "", "/taskSamples/"+sample.ID, VerificationMismatch, err.Error(), sample.ID))
		} else {
			check.Capabilities = []Capability{AsyncJobsCapability}
			key := sample.Kind + "/" + sample.Purpose
			coverage[sample.Operation][key] = true
			if sample.Kind == "decode" || sample.Kind == "encode" {
				state := output
				if sample.Kind == "encode" {
					state = sample.Input
				}
				var update JobUpdate
				if err := state.Decode(&update); err != nil {
					return false
				}
				coverage[sample.Operation][sample.Kind+"/state/"+string(update.Status)] = true
			}
		}
		report.Checks = append(report.Checks, check)
	}
	isComplete := len(coverage) > 0
	for name, covered := range coverage {
		required := []string{"decode/submit", "decode/status", "control/status", "control/result"}
		for _, state := range []TaskStatus{TaskQueued, TaskRunning, TaskCompleted, TaskFailed} {
			required = append(required, "decode/state/"+string(state))
		}
		for _, state := range []TaskStatus{TaskSubmitting, TaskUncertain, TaskQueued, TaskRunning, TaskCompleted, TaskFailed, TaskCancelled} {
			required = append(required, "encode/state/"+string(state))
		}
		if compiled.operations[name].Task.Cancel != "" {
			required = append(required, "decode/cancel", "decode/state/cancelled", "control/cancel")
		}
		for _, key := range required {
			if !covered[key] {
				isComplete = false
				report.Issues = append(report.Issues, verificationIssue(compiled, "", "/operations/"+name+"/task", IncompleteCoverage, "task flow lacks passing evidence for "+key, ""))
			}
		}
	}
	return isComplete
}
