package protocol

import "context"

// CapabilityContractHash binds paired evidence to the operator's complete model
// contract, including explicit false entries. A narrower profile cannot prove
// that an edited parent contract was verified.
func CapabilityContractHash(capabilities CapabilitySet) string {
	value, _ := EncodeValue(capabilities)
	return hashValue(value)
}

// VerifyBindingProfiles retains the complete contract's diagnostics and derives
// independently verified subsets for ordinary HTTP generation. It never changes
// the definition, fixtures or input. Runtime selects one entire passing request
// profile; evidence from separate profiles cannot be combined. The original
// upstream response is checked against the full model contract before conversion,
// then against the target's expression capabilities. Stateful sessions and jobs
// require their complete flow contract.
func VerifyBindingProfiles(ctx context.Context, ingress, upstream *Compiled, capabilities CapabilitySet, policies ...*CompiledConversion) []CombinationReport {
	full := VerifyBindingCombination(ctx, ingress, upstream, capabilities, policies...)
	reports := []CombinationReport{full}
	if full.Passed || !canProfileHTTP(ingress) || !canProfileHTTP(upstream) {
		return reports
	}
	base := CapabilitySet{}
	for _, capability := range []Capability{TextCapability, UsageCapability} {
		if capabilities[capability] && ingress.capabilities[capability] && upstream.capabilities[capability] {
			base[capability] = true
		}
	}
	verify := func(contract CapabilitySet) CombinationReport {
		report := VerifyBindingCombination(ctx, ingress, upstream, contract, policies...)
		report.BindingHash, report.IsRestricted = full.BindingHash, true
		return report
	}
	baseline := verify(base)
	if !baseline.Passed {
		return reports
	}
	profiles := []CombinationReport{baseline}
	combined := copyCapabilities(base)
	seen := map[string]bool{CapabilityContractHash(base): true}
	for _, check := range full.Checks {
		contract := copyCapabilities(base)
		isShared := true
		for _, capability := range check.Capabilities {
			if !capabilities[capability] || !ingress.capabilities[capability] || !upstream.capabilities[capability] || (capability == NativeExtensionsCapability && !hasSameWire(ingress, upstream)) {
				isShared = false
				break
			}
			contract[capability] = true
		}
		hash := CapabilityContractHash(contract)
		if !isShared || seen[hash] {
			continue
		}
		seen[hash] = true
		report := verify(contract)
		if report.Passed {
			profiles = append(profiles, report)
			for capability := range contract {
				combined[capability] = true
			}
		}
	}
	if len(profiles) == 1 {
		return append(reports, baseline)
	}
	if len(profiles) == 2 {
		return append(reports, profiles[1])
	}
	union := verify(combined)
	if union.Passed {
		return append(reports, union)
	}
	// Passing separate capabilities does not establish that their interactions
	// are supported. Keep the failed union and the narrower independent proofs.
	reports = append(reports, union)
	return append(reports, profiles...)
}

func canProfileHTTP(compiled *Compiled) bool {
	hasGeneration := false
	for _, operation := range compiled.operations {
		if operation.Kind == "models" {
			continue
		}
		if operation.Kind != "generate" || operation.Transport == WebSocket {
			return false
		}
		hasGeneration = true
	}
	return hasGeneration
}

func copyCapabilities(source CapabilitySet) CapabilitySet {
	copy := make(CapabilitySet, len(source))
	for capability, supported := range source {
		copy[capability] = supported
	}
	return copy
}
