package protocol

// verificationSamples reads the authored fixture set without converting a
// package into a legacy definition. The returned documents are independent.
func (compiled *Compiled) verificationSamples() []Sample {
	if compiled.packagePlan != nil {
		document, _ := compiled.Package()
		return document.Mapping.Samples
	}
	return compiled.Definition().Samples
}
