package protocol

const (
	objectReuseReads     = 2
	objectReaderChildren = 8
)

// Each reader lives within its input or iteration item's existing lifetime.
// Parent readers describe enclosing active scopes, never previously visited items.
type planObjectReader struct {
	root     immutableSpan
	fields   Object
	parent   *planObjectReader
	children []objectReadView
}

type objectReadView struct {
	identity immutableSpan
	fields   Object
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
func (reader *planObjectReader) fieldsFor(value Value) Object {
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
	return fields[key], err
}

// Only the current root and its bounded descendants share a field table. Values
// and plain keys borrow immutable input; consumers must not mutate these maps.
func (reader *planObjectReader) readFields(value Value, borrowed *valueReader) (Object, error) {
	if reader == nil {
		return value.readObjectFieldsWithin(borrowed, true)
	}
	identity := spanIdentity(value.raw)
	if reader.root == identity {
		if reader.fields == nil {
			fields, err := value.readObjectFieldsWithin(borrowed, true)
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
	fields, err := value.readObjectFieldsWithin(borrowed, true)
	if err != nil {
		return nil, err
	}
	if len(reader.children) < objectReaderChildren {
		if reader.children == nil {
			reader.children = make([]objectReadView, 0, objectReaderChildren)
		}
		reader.children = append(reader.children, objectReadView{identity: identity, fields: fields})
	}
	return fields, nil
}

func (reader *planObjectReader) releaseItem(_ error) {
	if reader == nil {
		return
	}
	*reader = planObjectReader{}
}
