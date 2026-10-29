package protocol

import "github.com/bytedance/sonic/ast"

const objectReuseReads = 2

// Each reader lives within its input or iteration item's existing lifetime.
// Parent readers describe enclosing active scopes, never previously visited items.
type planObjectReader struct {
	root     immutableSpan
	node     ast.Node
	parent   *planObjectReader
	isLoaded bool
}

func (plan *compiledOutputPlan) newRootObjectReader(input Value) *planObjectReader {
	return newPlanObjectReader(input, int(plan.rootObjectReads))
}

func newPlanObjectReader(input Value, readCount int) *planObjectReader {
	if readCount < objectReuseReads || !input.IsObject() {
		return nil
	}
	return &planObjectReader{root: spanIdentity(input.raw)}
}

func (reader *planObjectReader) readMember(value Value, key string) (Value, error) {
	identity := spanIdentity(value.raw)
	for ; reader != nil; reader = reader.parent {
		if reader.root != identity {
			continue
		}
		if !reader.isLoaded {
			reader.node = ast.NewRaw(value.raw)
			reader.isLoaded = true
		}
		return readSonicObjectMember(&reader.node, key)
	}
	return sonicObjectMember(value, key)
}

func (reader *planObjectReader) releaseItem(err error) {
	if reader == nil {
		return
	}
	if err != nil {
		// Sonic errors can reference node itself; release enclosing owners without
		// rewriting the node retained by that error.
		reader.root = immutableSpan{}
		reader.parent = nil
		return
	}
	*reader = planObjectReader{}
}
