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

func (compiled *Compiled) verificationModelSamples() []ModelSample {
	if compiled.packagePlan != nil {
		document, _ := compiled.Package()
		return document.Mapping.ModelSamples
	}
	return compiled.Definition().ModelSamples
}

func (compiled *Compiled) verificationTaskSamples() []TaskSample {
	if compiled.packagePlan != nil {
		document, _ := compiled.Package()
		return document.Mapping.TaskSamples
	}
	return compiled.Definition().TaskSamples
}

func (compiled *Compiled) verificationAgentSamples() []AgentSample {
	if compiled.packagePlan != nil {
		document, _ := compiled.Package()
		return document.Mapping.Agent.Samples
	}
	return compiled.Definition().Agent.Samples
}

func (compiled *Compiled) verificationSessionSamples() []SessionSample {
	if compiled.packagePlan != nil {
		document, _ := compiled.Package()
		return document.Mapping.SessionSamples
	}
	return compiled.Definition().SessionSamples
}

func (compiled *Compiled) sessionSamplesPath() string {
	if compiled.packagePlan != nil {
		return "/mapping/sessionSamples"
	}
	return "/sessionSamples"
}
