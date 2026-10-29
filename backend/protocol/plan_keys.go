package protocol

import (
	"fmt"
	"slices"

	"github.com/bytedance/sonic/ast"
)

func (plan *compiledOutputPlan) buildKeys(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	keys, err := outputObjectKeys(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	bytes := 2
	for index, key := range keys {
		if index != 0 {
			bytes++
		}
		bytes += jsonStringBytes(key)
		if bytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
	}
	// Measure escaping before allocating encoded keys: a large HTML key can
	// otherwise expand beyond the budget even when its source object fits.
	node := &jsonOutputNode{items: make([]JSONOutput, len(keys))}
	for index, key := range keys {
		node.items[index] = JSONLeaf(StringValue(key))
	}
	return plannedOutput{output: JSONOutput{node: node}, bytes: bytes}, nil
}

// outputObjectKeys never materializes or normalizes member values. buildKeys
// copies only these names into its independent result.
func outputObjectKeys(output JSONOutput) ([]string, error) {
	var keys []string
	if output.node != nil {
		if !output.node.object {
			return nil, fmt.Errorf("expected JSON object")
		}
		keys = make([]string, 0, len(output.node.fields))
		for _, field := range output.node.fields {
			keys = append(keys, coreJSONKey(field.key))
		}
	} else {
		if !output.value.IsObject() {
			return nil, fmt.Errorf("expected JSON object")
		}
		node := ast.NewRaw(output.value.raw)
		properties, err := node.Properties()
		if err != nil {
			return nil, err
		}
		var pair ast.Pair
		for properties.Next(&pair) {
			keys = append(keys, coreJSONKey(pair.Key))
		}
		if err := node.Check(); err != nil {
			return nil, err
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}
