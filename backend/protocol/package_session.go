package protocol

import "fmt"

// ProtocolSessionSpec declares both external frame lanes and transport bounds.
// Command association and lifecycle remain owned by the existing SessionReplay.
type ProtocolSessionSpec struct {
	SessionConfig
	ClientEvent string `json:"clientEvent"`
	ServerEvent string `json:"serverEvent"`
}

func (compiled *Compiled) bindPackageSession(spec ProtocolOperationSpec, binding ProtocolOperationMapping, directions map[Direction]ProtocolDirectionMapping) error {
	if spec.Request == "" || spec.Response != "" || binding.Response != "" || (binding.Request != DecodeRequest && binding.Request != EncodeRequest) {
		return fmt.Errorf("session requires a handshake request mapping; frame lanes belong to session documents")
	}
	if !compiled.Supports(binding.Request) {
		return fmt.Errorf("session handshake mapping is not implemented")
	}
	canIngress := compiled.Supports(DecodeRequest) && compiled.Supports(DecodeClientEvent) && compiled.Supports(EncodeEvent)
	canUpstream := compiled.Supports(EncodeRequest) && compiled.Supports(EncodeUpstreamEvent) && compiled.Supports(DecodeEvent)
	operation := Operation{Session: &spec.Session.SessionConfig}
	if err := checkSessionAdapters(operation, compiled.capabilities, canIngress, canUpstream); err != nil {
		return err
	}
	for _, lane := range []struct {
		document   string
		directions []Direction
	}{
		{spec.Request, []Direction{DecodeRequest, EncodeRequest}},
		{spec.Session.ClientEvent, []Direction{DecodeClientEvent, EncodeUpstreamEvent}},
		{spec.Session.ServerEvent, []Direction{DecodeEvent, EncodeEvent}},
	} {
		for _, direction := range lane.directions {
			entry, exists := directions[direction]
			if !exists {
				continue
			}
			if lane.document == "" || entry.Document != lane.document || compiled.wireContracts[direction].packageStream != nil {
				return fmt.Errorf("session %s must bind its declared lane document, without a single-response stream contract", direction)
			}
		}
	}
	return nil
}

func (compiled *Compiled) hasPackageSession() bool {
	for _, operation := range compiled.operations {
		if operation.Transport == WebSocket {
			return true
		}
	}
	return false
}
