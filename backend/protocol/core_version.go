package protocol

// CoreVersion identifies the central semantic contract used by this package.
func (compiled *Compiled) CoreVersion() int {
	return compiled.packagePlan.coreVersion
}
