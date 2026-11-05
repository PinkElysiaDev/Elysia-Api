package protocol

import "fmt"

func (compiled *Compiled) bindFinalDocument(direction Direction, mapping ProtocolDirectionMapping, documents map[string]compiledWireContract) error {
	if mapping.FinalDocument == "" {
		return nil
	}
	path := "/mapping/directions/" + string(direction) + "/finalDocument"
	if direction != EncodeRequest && direction != EncodeResponse && direction != EncodeEvent {
		return fmt.Errorf("%s: requires request, response or event encoding", path)
	}
	contract, exists := documents[mapping.FinalDocument]
	if !exists {
		return fmt.Errorf("%s: requires a declared wire document", path)
	}
	if (contract.packageStream != nil) != (direction == EncodeEvent) {
		return fmt.Errorf("%s: document lifecycle must match the encoding direction", path)
	}
	if compiled.packagePlan.finalContracts == nil {
		compiled.packagePlan.finalContracts = make(map[Direction]compiledWireContract)
	}
	compiled.packagePlan.finalContracts[direction] = contract
	return nil
}

func (compiled *Compiled) selectFinalWireContract(direction Direction) compiledWireContract {
	if compiled.packagePlan != nil {
		if contract, exists := compiled.packagePlan.finalContracts[direction]; exists {
			return contract
		}
	}
	return compiled.wireContracts[direction]
}
