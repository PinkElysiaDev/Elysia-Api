package builtin

import (
	"sync"

	p "github.com/elysia-api/backend/protocol"
)

// Compiled revisions are immutable; Definition returns an independent editable
// copy and each request/stream owns its evaluation state. Compile fixture inputs
// once instead of recompiling all preceding presets in every matrix cell.
// Compiler and definition verification tests still invoke the compiler directly.
var shippedTestProtocols = sync.OnceValues(func() (map[string]*p.Compiled, error) {
	definitions, err := Definitions()
	if err != nil {
		return nil, err
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*p.Compiled, len(definitions))
	for _, value := range definitions {
		compiled, issues := compiler.Compile(value.Bytes())
		if err := p.IssuesError(issues); err != nil {
			return nil, err
		}
		result[compiled.Codec(p.EncodeRequest)] = compiled
	}
	return result, nil
})
