package protocol

import "github.com/bytedance/sonic/ast"

const (
	objectReuseReads     = 2
	objectReaderChildren = 8
)

// Each reader lives within its input or iteration item's existing lifetime.
// Parent readers describe enclosing active scopes, never previously visited items.
type planObjectReader struct {
	root     immutableSpan
	node     ast.Node
	parent   *planObjectReader
	isLoaded bool
	children []objectReadView
}

type objectReadView struct {
	identity immutableSpan
	node     ast.Node
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
	return reader.ownerFor(value).readOwnedMember(value, key)
}

func (reader *planObjectReader) ownerFor(value Value) *planObjectReader {
	identity := spanIdentity(value.raw)
	for ; reader != nil; reader = reader.parent {
		if reader.root == identity {
			return reader
		}
	}
	return nil
}

func (reader *planObjectReader) readOwnedMember(value Value, key string) (Value, error) {
	if reader == nil {
		return sonicObjectMember(value, key)
	}
	identity := spanIdentity(value.raw)
	if reader.root == identity {
		if !reader.isLoaded {
			reader.node = ast.NewRaw(value.raw)
			reader.isLoaded = true
		}
		return readSonicObjectMember(&reader.node, key)
	}
	for index := range reader.children {
		child := &reader.children[index]
		if child.identity == identity {
			return readSonicObjectMember(&child.node, key)
		}
	}
	// Only descendants reached through this active owner enter the bounded
	// cache. A large history cannot retain one parse tree per visited node.
	if len(reader.children) < objectReaderChildren {
		if reader.children == nil {
			reader.children = make([]objectReadView, 0, objectReaderChildren)
		}
		reader.children = append(reader.children, objectReadView{identity: identity, node: ast.NewRaw(value.raw)})
		return readSonicObjectMember(&reader.children[len(reader.children)-1].node, key)
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
		reader.children = nil
		return
	}
	*reader = planObjectReader{}
}
