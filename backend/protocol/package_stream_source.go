package protocol

import "context"

// sourceLayout records provenance from capture_source or capture_native.source.
// Plain JSON cannot acquire a receipt, and Native envelope authority is not kept.
type sourceLayout struct {
	isSource bool
	fields   map[string]*sourceLayout
	items    map[int]*sourceLayout
}

func indexSourceLayout(output JSONOutput) *sourceLayout {
	if output.node == nil {
		return nil
	}
	if output.node.origin == capturedProvenanceOrigin {
		return &sourceLayout{isSource: true}
	}
	var layout *sourceLayout
	for _, field := range output.node.fields {
		if child := indexSourceLayout(field.value); child != nil {
			if layout == nil {
				layout = &sourceLayout{fields: make(map[string]*sourceLayout)}
			}
			layout.fields[field.key] = child
		}
	}
	for index, item := range output.node.items {
		if child := indexSourceLayout(item); child != nil {
			if layout == nil {
				layout = &sourceLayout{items: make(map[int]*sourceLayout)}
			}
			layout.items[index] = child
		}
	}
	return layout
}

func retainIndexSources(ctx context.Context, output JSONOutput, owned Value) (JSONOutput, error) {
	layout := indexSourceLayout(output)
	if layout == nil {
		return JSONOutput{}, nil
	}
	return layout.restore(ctx, JSONLeaf(owned))
}

// Rebuild only containers that lead to captured sources. Their leaves borrow
// the already-owned index JSON, never the original frame or a temporary plan.
func (layout *sourceLayout) restore(ctx context.Context, output JSONOutput) (JSONOutput, error) {
	if err := ctx.Err(); err != nil {
		return JSONOutput{}, err
	}
	if layout.isSource || layout.fields != nil {
		fields, err := plannedObjectFields(output)
		if err != nil {
			return JSONOutput{}, err
		}
		for index := range fields {
			if child := layout.fields[fields[index].key]; child != nil {
				fields[index].value, err = child.restore(ctx, fields[index].value)
				if err != nil {
					return JSONOutput{}, err
				}
			}
		}
		node := &jsonOutputNode{object: true, fields: fields}
		if layout.isSource {
			node.origin = capturedProvenanceOrigin
		}
		return JSONOutput{node: node}, nil
	}
	items, err := plannedArrayItems(output)
	if err != nil {
		return JSONOutput{}, err
	}
	for index, child := range layout.items {
		items[index], err = child.restore(ctx, items[index])
		if err != nil {
			return JSONOutput{}, err
		}
	}
	return JSONOutput{node: &jsonOutputNode{items: items}}, nil
}

func (transaction *packageStreamTransaction) indexOutput(slot int, key string, value Value) JSONOutput {
	if transaction.indexSourceUpdates != nil {
		if source, exists := transaction.indexSourceUpdates[slot][key]; exists {
			if !source.IsZero() {
				return source
			}
			return JSONLeaf(value)
		}
	}
	if transaction.previous != nil && transaction.previous.indexSources != nil {
		if source, exists := transaction.previous.indexSources[slot][key]; exists {
			return source
		}
	}
	return JSONLeaf(value)
}

func (transaction *packageStreamTransaction) commitIndexSources() []map[string]JSONOutput {
	var sources []map[string]JSONOutput
	if transaction.previous != nil {
		sources = transaction.previous.indexSources
	}
	for slot, updates := range transaction.indexSourceUpdates {
		if len(updates) == 0 {
			continue
		}
		if sources == nil {
			sources = make([]map[string]JSONOutput, len(transaction.plan.indexes))
		}
		for key, source := range updates {
			if source.IsZero() {
				delete(sources[slot], key)
				continue
			}
			if sources[slot] == nil {
				sources[slot] = make(map[string]JSONOutput)
			}
			sources[slot][key] = source
		}
	}
	return sources
}
