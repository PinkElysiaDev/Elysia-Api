package protocol

import (
	"context"
	"fmt"
)

func executeTokenCountSample(ctx context.Context, compiled *Compiled, sample Sample) (verificationResult, error) {
	count, err := compiled.tokenCounting(sample.Operation)
	if err != nil {
		return verificationResult{}, err
	}
	if sample.Direction == DecodeRequest || sample.Direction == EncodeRequest {
		return executeVerificationSample(ctx, count.request, sample)
	}
	var result TokenCount
	var output Value
	switch sample.Direction {
	case DecodeResponse:
		result, err = compiled.DecodeTokenCount(ctx, sample.Operation, sample.Input)
		if err == nil {
			output, err = EncodeValue(result)
		}
	case EncodeResponse:
		result, err = count.readResult(sample.Input, compiled.limits)
		if err == nil {
			output, err = compiled.EncodeTokenCount(ctx, sample.Operation, result)
		}
	default:
		err = fmt.Errorf("token counting requires a request or response direction")
	}
	return verificationResult{output: output, semantic: result}, err
}

func verifyTokenCountSamples(ctx context.Context, compiled *Compiled, report *VerificationReport) CapabilitySet {
	capabilities := CapabilitySet{}
	if compiled.packagePlan == nil || len(compiled.packagePlan.tokenCounts) == 0 {
		return capabilities
	}
	document, _ := compiled.Package()
	covered := make(map[string]map[Direction]bool)
	for index, sample := range document.Mapping.TokenCountSamples {
		path := fmt.Sprintf("/mapping/tokenCountSamples/%d", index)
		result, err := executeTokenCountSample(ctx, compiled, sample)
		expected := sample.Expected
		if err == nil && sample.Direction == DecodeRequest && sample.ExpectedIssue == "" {
			expected, err = comparableExpected(compiled, sample)
		}
		if err == nil && sample.ExpectedIssue == "" && (sample.Direction == DecodeRequest || sample.Direction == EncodeRequest) {
			err = verifyRoundTrip(ctx, compiled.packagePlan.tokenCounts[sample.Operation].request, sample, result)
		}
		passed := err == nil && equalValues(result.output, expected)
		if sample.ExpectedIssue != "" {
			passed = hasIssueCode(err, sample.ExpectedIssue)
		}
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Direction: sample.Direction, Passed: passed})
		if !passed {
			if err != nil && sample.ExpectedIssue == "" {
				report.Issues = append(report.Issues, sampleIssues(compiled, sample, path, err)...)
			} else {
				report.Issues = append(report.Issues, verificationIssue(compiled, sample.Direction, path+"/expected", VerificationMismatch, "token counting differs from the declared assertion", sample.ID))
			}
		} else if sample.ExpectedIssue == "" {
			if covered[sample.Operation] == nil {
				covered[sample.Operation] = make(map[Direction]bool)
			}
			covered[sample.Operation][sample.Direction] = true
			for capability := range observeCapabilities(result.semantic).observed {
				capabilities[capability] = true
			}
		}
	}
	for _, name := range sortedKeys(compiled.packagePlan.tokenCounts) {
		for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
			if !covered[name][direction] {
				report.Issues = append(report.Issues, verificationIssue(compiled, direction, "/mapping/operations/"+name+"/countTokens", IncompleteCoverage, "token counting requires passing evidence in all four directions", ""))
			}
		}
	}
	return capabilities
}
