//go:build semanticmeter_bootstrap

package protocol

// Generation builds retain the reflective contract, allowing stale generated
// field references to be excluded while compiling the current model.
func (meter *typedMeter) visitTyped(value any) (bool, bool) { return false, false }
