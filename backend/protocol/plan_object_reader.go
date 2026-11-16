package protocol

const (
	objectReuseReads            = 2
	objectReaderChildren        = 8
	objectReaderInitialChildren = 2
)

// Each reader lives within its input or iteration item's existing lifetime.
// Parent readers describe enclosing active scopes, never previously visited items.
type planObjectReader struct {
	root     immutableSpan
	fields   *objectFieldView
	parent   *planObjectReader
	children []objectReadView
}

type objectReadView struct {
	identity immutableSpan
	fields   *objectFieldView
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

func (reader *planObjectReader) ownerFor(value Value) *planObjectReader {
	identity := spanIdentity(value.raw)
	for ; reader != nil; reader = reader.parent {
		if reader.root == identity {
			return reader
		}
	}
	return nil
}

// Entering a nested scope may reuse a table already held by an active owner.
// The child owns only its reader; the shared field table stays immutable.
func (reader *planObjectReader) fieldsFor(value Value) *objectFieldView {
	identity := spanIdentity(value.raw)
	for ; reader != nil; reader = reader.parent {
		if reader.root == identity {
			return reader.fields
		}
		for _, child := range reader.children {
			if child.identity == identity {
				return child.fields
			}
		}
	}
	return nil
}

func (reader *planObjectReader) readOwnedMember(value Value, key string) (Value, error) {
	if reader == nil {
		if value.Len() <= objectMemberScanBytes {
			return scanObjectMember(value, key)
		}
		return sonicObjectMember(value, key)
	}
	fields, err := reader.readFields(value, &valueReader{})
	if err != nil {
		return Value{}, err
	}
	selected, _ := fields.readMember(key)
	return selected, nil
}

// Only the current root and its bounded descendants share a field table. Values
// and plain keys borrow immutable input; consumers must not mutate these views.
func (reader *planObjectReader) readFields(value Value, borrowed *valueReader) (*objectFieldView, error) {
	if reader == nil {
		return value.readObjectView(borrowed)
	}
	identity := spanIdentity(value.raw)
	if reader.root == identity {
		if reader.fields == nil {
			fields, err := value.readObjectView(borrowed)
			reader.fields = fields
			return fields, err
		}
		return reader.fields, nil
	}
	for _, child := range reader.children {
		if child.identity == identity {
			return child.fields, nil
		}
	}
	fields, err := value.readObjectView(borrowed)
	if err != nil {
		return nil, err
	}
	reader.cacheChild(objectReadView{identity: identity, fields: fields})
	return fields, nil
}

func (reader *planObjectReader) cacheChild(child objectReadView) {
	if len(reader.children) >= objectReaderChildren {
		return
	}
	if len(reader.children) == cap(reader.children) {
		capacity := objectReaderChildren
		if len(reader.children) == 0 {
			capacity = objectReaderInitialChildren
		}
		children := make([]objectReadView, len(reader.children), capacity)
		copy(children, reader.children)
		reader.children = children
	}
	reader.children = append(reader.children, child)
}

func (reader *planObjectReader) releaseItem(_ error) {
	if reader == nil {
		return
	}
	*reader = planObjectReader{}
}
