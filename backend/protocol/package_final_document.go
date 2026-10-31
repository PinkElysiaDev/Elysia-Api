package protocol

import "fmt"

func (compiled *Compiled) bindFinalDocument(direction Direction, mapping ProtocolDirectionMapping, spec ProtocolSpec, documents map[string]compiledWireContract) (*WireContractSpec, error) {
	if mapping.FinalDocument == "" {
		return nil, nil
	}
	path := "/mapping/directions/" + string(direction) + "/finalDocument"
	if direction != EncodeRequest && direction != EncodeResponse && direction != EncodeEvent {
		return nil, fmt.Errorf("%s: requires request, response or event encoding", path)
	}
	contract, exists := documents[mapping.FinalDocument]
	if !exists {
		return nil, fmt.Errorf("%s: requires a declared wire document", path)
	}
	if (contract.packageStream != nil) != (direction == EncodeEvent) {
		return nil, fmt.Errorf("%s: document lifecycle must match the encoding direction", path)
	}
	if compiled.packagePlan.finalContracts == nil {
		compiled.packagePlan.finalContracts = make(map[Direction]compiledWireContract)
	}
	compiled.packagePlan.finalContracts[direction] = contract
	wire := spec.Documents[mapping.FinalDocument]
	return &wire, nil
}

func (compiled *Compiled) selectFinalWireContract(direction Direction) compiledWireContract {
	if compiled.packagePlan != nil {
		if contract, exists := compiled.packagePlan.finalContracts[direction]; exists {
			return contract
		}
	}
	return compiled.wireContracts[direction]
}
