package protocol

// CoreVersion identifies the semantic contract pinned by this compiled mapping.
// Legacy definitions continue to use the original core contract.
func (compiled *Compiled) CoreVersion() int {
	if compiled.packagePlan != nil {
		return compiled.packagePlan.coreVersion
	}
	return SemanticSchemaVersion
}

func (target Target) coreSchemaVersion() int {
	// Public Target literals predate versioned packages and retain core1 checks.
	if target.coreVersion == 0 {
		return SemanticSchemaVersion
	}
	return target.coreVersion
}
