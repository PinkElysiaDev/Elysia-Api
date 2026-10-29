package protocol

import "github.com/bytedance/sonic/ast"

const rootObjectReuseReads = 2

// Only the original input is retained: it already lives through this evaluation.
// Temporary item and binding containers must not extend their owners' lifetime.
type planObjectReader struct {
	root     immutableSpan
	node     ast.Node
	isLoaded bool
}

func (plan *compiledOutputPlan) newRootObjectReader(input Value) *planObjectReader {
	if plan.rootObjectReads < rootObjectReuseReads || !input.IsObject() {
		return nil
	}
	return &planObjectReader{root: spanIdentity(input.raw)}
}

func (reader *planObjectReader) readMember(value Value, key string) (Value, error) {
	if reader == nil || reader.root != spanIdentity(value.raw) {
		return sonicObjectMember(value, key)
	}
	if !reader.isLoaded {
		reader.node = ast.NewRaw(value.raw)
		reader.isLoaded = true
	}
	return readSonicObjectMember(&reader.node, key)
}
