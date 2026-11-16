package protocol

// Compilation already validates the immutable definition. Borrow its selected
// fixture bytes only during decoding; returned maps, slices and Values are owned.
func readVerificationSamples[T any](compiled *Compiled, path ...string) []T {
	value, _ := readValuePointerWithin(compiled.definition, path, &valueReader{isBorrowed: true})
	var samples []T
	if !value.IsZero() {
		_ = value.Decode(&samples)
	}
	return samples
}

func (compiled *Compiled) verificationSamples() []Sample {
	return readVerificationSamples[Sample](compiled, "mapping", "samples")
}

func (compiled *Compiled) verificationModelSamples() []ModelSample {
	return readVerificationSamples[ModelSample](compiled, "mapping", "modelSamples")
}

func (compiled *Compiled) verificationTaskSamples() []TaskSample {
	return readVerificationSamples[TaskSample](compiled, "mapping", "taskSamples")
}

func (compiled *Compiled) verificationAgentSamples() []AgentSample {
	return readVerificationSamples[AgentSample](compiled, "mapping", "agent", "samples")
}

func (compiled *Compiled) verificationSessionSamples() []SessionSample {
	return readVerificationSamples[SessionSample](compiled, "mapping", "sessionSamples")
}

func (compiled *Compiled) verificationTokenCountSamples() []Sample {
	return readVerificationSamples[Sample](compiled, "mapping", "tokenCountSamples")
}

func (compiled *Compiled) sessionSamplesPath() string {
	return "/mapping/sessionSamples"
}
