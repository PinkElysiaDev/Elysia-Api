package protocol

import "maps"

// DeclaredCapabilities returns an independent copy of the revision's capability
// set without decoding its authored configuration or selecting one direction.
func (compiled *Compiled) DeclaredCapabilities() CapabilitySet {
	return maps.Clone(compiled.capabilities)
}
