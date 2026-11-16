package protocol

import "context"

type contextualWireValidator interface {
	validate(context.Context, Direction, Value, Limits) error
}

// compiledWireContract is pinned independently of the execution mapping. Its
// validators check the delivered wire independently of the Mapping program.
type compiledWireContract struct {
	validator        WireOutputValidator
	contextValidator contextualWireValidator
	packageStream    *compiledPackageStreamContract
}
