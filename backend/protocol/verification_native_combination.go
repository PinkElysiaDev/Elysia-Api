package protocol

import (
	"context"
	"errors"
)

// Native extension support is conditional on wire identity. Every composition
// tests that boundary: incompatible protocols must reject the probe explicitly.
func verifyCombinationNative(ctx context.Context, ingress, upstream *Compiled, report *CombinationReport) {
	if !ingress.native.Preserve || !upstream.native.Preserve || !ingress.capabilities[NativeExtensionsCapability] || !upstream.capabilities[NativeExtensionsCapability] {
		return
	}
	for _, sample := range ingress.Definition().Samples {
		if sample.Direction != DecodeRequest || sample.ExpectedIssue != "" {
			continue
		}
		fields, err := sample.Input.ReadObject()
		if err != nil {
			return
		}
		probe, err := ParseValue([]byte(`{"null":null,"zero":0,"flag":false,"long":9007199254740993}`))
		if err != nil {
			return
		}
		fields[nativeProbeField] = probe
		sample.Input, err = EncodeValue(fields)
		if err != nil {
			return
		}
		err = verifyRequestCombination(ctx, ingress, upstream, sample)
		check := VerificationCheck{SampleID: "native-extension-boundary", Direction: EncodeRequest}
		if hasSameWire(ingress, upstream) {
			check.Passed = err == nil
			check.Capabilities = []Capability{NativeExtensionsCapability}
		} else {
			var conversion *ConversionError
			if errors.As(err, &conversion) {
				for _, issue := range conversion.Issues {
					check.Passed = check.Passed || issue.Code == UnsupportedNative || issue.Code == UnsupportedCapability
				}
			}
			check.Reason = "foreign native extension must produce an explicit conversion diagnostic"
		}
		if !check.Passed {
			report.Issues = append(report.Issues, verificationIssue(upstream, EncodeRequest, "/combination/native", VerificationMismatch, "native extension boundary did not preserve same-wire data or reject foreign data", sample.ID))
		}
		report.Checks = append(report.Checks, check)
		return
	}
}

func hasSameWire(left, right *Compiled) bool {
	return left.identity.Family == right.identity.Family && left.identity.WireVersion == right.identity.WireVersion
}
