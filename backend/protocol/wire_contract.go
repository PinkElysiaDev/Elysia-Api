package protocol

// compiledWireContract is pinned independently of the execution mapping. Its
// validators check the delivered wire even when a mapping replaces the codec.
type compiledWireContract struct {
	codec           string
	validator       WireOutputValidator
	objectValidator WireObjectOutputValidator
	streamFactory   StreamModule
}

// bindModuleWireContract captures the current module contracts during compiler
// migration. ProtocolSpec compilation will supply these bindings directly.
func bindModuleWireContract(module Module) compiledWireContract {
	if module == nil {
		return compiledWireContract{}
	}
	contract := compiledWireContract{codec: module.Name()}
	contract.validator, _ = module.(WireOutputValidator)
	contract.objectValidator, _ = module.(WireObjectOutputValidator)
	// Existing custom modules have not declared the built-in wire lifecycle.
	// Preserve that boundary while moving selection out of request execution.
	if knownConversionCodec(contract.codec) {
		contract.streamFactory, _ = module.(StreamModule)
	}
	return contract
}
