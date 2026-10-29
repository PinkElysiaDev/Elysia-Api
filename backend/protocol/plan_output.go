package protocol

import (
	"context"
	"fmt"
)

// compiledOutputPlan keeps construction nodes unencoded until the mapping
// returns. Eligibility is decided before execution; a failed plan is never rerun.
type compiledOutputPlan struct {
	expression   *compiledExpression
	fields       []outputPlanField
	items        []*compiledOutputPlan
	source       *compiledOutputPlan
	body         *compiledOutputPlan
	when         *compiledOutputPlan
	then         *compiledOutputPlan
	otherwise    *compiledOutputPlan
	cases        map[string]*compiledOutputPlan
	bindingSlots int
}

type outputPlanField struct {
	key        string
	keyBytes   int
	expression *compiledOutputPlan
}

func (plan *compiledOutputPlan) isInputIdentity() bool {
	return plan != nil && plan.expression.isInputIdentity()
}

func (plan *compiledOutputPlan) checkIdentityAfter(ctx context.Context, bytes int, limits Limits) error {
	if plan == nil {
		return nil
	}
	return plan.expression.checkIdentityAfter(ctx, bytes, limits)
}

func compileOutputPlan(expression *compiledExpression) (*compiledOutputPlan, bool) {
	if expression == nil {
		return nil, true
	}
	plan := &compiledOutputPlan{expression: expression}
	switch expression.op {
	case "omit", "literal", "read", "let", "index", "object", "project", "array", "map", "flatmap", "filter", "if", "choose",
		"merge", "concat", "join", "sort", "associate", "parse_json", "stringify_json",
		"exists", "present", "type", "is_integer", "integer", "not", "enum", "cast", "strip_prefix", "trim_prefix", "equal", "all", "any":
	default:
		return nil, false
	}
	if expression.op == "let" || expression.op == "read" && expression.from == "binding" {
		plan.bindingSlots = expression.bindingSlot + 1
	}
	for _, child := range []struct {
		source *compiledExpression
		target **compiledOutputPlan
	}{{expression.source, &plan.source}, {expression.body, &plan.body}, {expression.when, &plan.when}, {expression.then, &plan.then}, {expression.otherwise, &plan.otherwise}} {
		compiled, ok := compileOutputPlan(child.source)
		if !ok {
			return nil, false
		}
		*child.target = compiled
		if compiled != nil {
			plan.bindingSlots = max(plan.bindingSlots, compiled.bindingSlots)
		}
	}
	if expression.op == "object" {
		for _, key := range expression.keys {
			child, ok := compileOutputPlan(expression.fields[key])
			if !ok {
				return nil, false
			}
			plan.fields = append(plan.fields, outputPlanField{key, jsonStringBytes(key), child})
			plan.bindingSlots = max(plan.bindingSlots, child.bindingSlots)
		}
	}
	for _, item := range expression.items {
		child, ok := compileOutputPlan(item)
		if !ok {
			return nil, false
		}
		plan.items = append(plan.items, child)
		plan.bindingSlots = max(plan.bindingSlots, child.bindingSlots)
	}
	if len(expression.cases) != 0 {
		plan.cases = make(map[string]*compiledOutputPlan, len(expression.cases))
		for key, branch := range expression.cases {
			child, ok := compileOutputPlan(branch)
			if !ok {
				return nil, false
			}
			plan.cases[key] = child
			plan.bindingSlots = max(plan.bindingSlots, child.bindingSlots)
		}
	}
	return plan, true
}

type plannedOutput struct {
	output JSONOutput
	bytes  int
}

func (plan *compiledOutputPlan) evaluate(state evaluation) (Value, error) {
	value, err := plan.build(state)
	if err != nil || value.output.IsZero() {
		return Value{}, err
	}
	if value.output.node == nil {
		return value.output.value, nil
	}
	// Cancellation belongs to the same expression boundaries as the original
	// evaluator. Final materialization must not change its error precedence.
	return value.output.Encode(context.WithoutCancel(state.ctx))
}

func (plan *compiledOutputPlan) build(state evaluation) (value plannedOutput, err error) {
	if plan.bindingSlots != 0 && state.bindings == nil {
		state.bindings = make([]JSONOutput, plan.bindingSlots)
	}
	if plan.expression.op == "read" && state.hasOutputReader(plan.expression.from) {
		return plan.expression.buildReadOutput(state)
	}
	expression := plan.expression
	defer func() { err = locateMappingError(expression.path, err) }()
	if err := state.advance(); err != nil {
		return plannedOutput{}, err
	}
	defer func() {
		if err == nil && value.bytes > state.budget.limits.BufferBytes {
			err = resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, value.bytes)
			value = plannedOutput{}
		}
	}()
	switch expression.op {
	case "omit":
		return plannedOutput{}, nil
	case "literal":
		return plannedLeaf(expression.value), nil
	case "read":
		return plan.buildReadValue(state)
	case "let":
		return plan.buildBinding(state)
	case "index":
		return plannedLeaf(coreInteger(int64(state.itemIndex))), nil
	case "object":
		return plan.buildObject(state)
	case "project":
		return plan.buildProjection(state)
	case "array":
		return plan.buildArray(state)
	case "merge", "concat":
		return plan.buildOperands(state)
	case "join":
		return plan.buildJoin(state)
	case "sort", "associate":
		return plan.buildKeyedArray(state)
	case "parse_json", "stringify_json":
		return plan.buildJSONScalar(state)
	case "exists", "present", "not", "enum", "cast", "strip_prefix", "trim_prefix":
		return plan.buildScalar(state)
	case "type", "is_integer":
		return plan.buildTypeQuery(state)
	case "integer":
		return plan.buildInteger(state)
	case "equal", "all", "any":
		return plan.buildScalarOperands(state)
	case "map", "flatmap":
		return plan.buildIteration(state)
	case "filter":
		return plan.buildFilter(state)
	case "if":
		condition, err := plan.when.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		isTrue, err := readPlannedBoolean(condition.output)
		if err != nil {
			return plannedOutput{}, fmt.Errorf("%s/when: %w", expression.path, err)
		}
		branch := plan.otherwise
		if isTrue {
			branch = plan.then
		}
		if branch == nil {
			return plannedOutput{}, nil
		}
		return branch.build(state)
	case "choose":
		discriminator, err := plan.source.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		key, err := readPlannedString(discriminator.output)
		if err != nil {
			return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
		}
		branch := plan.cases[key]
		if branch == nil {
			branch = plan.otherwise
		}
		if branch == nil {
			return plannedOutput{}, fmt.Errorf("%s/cases: no branch for discriminator", expression.path)
		}
		return branch.build(state)
	}
	panic("uncompiled output operation")
}

func (plan *compiledOutputPlan) buildObject(state evaluation) (plannedOutput, error) {
	node := &jsonOutputNode{object: true, fields: make([]jsonOutputField, 0, len(plan.fields))}
	logicalBytes, encodedBytes := 0, 2
	for _, field := range plan.fields {
		value, err := field.expression.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		if value.output.IsZero() {
			continue
		}
		logicalBytes += value.bytes + len(field.key) + 4
		if logicalBytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		value = value.normalizeForContainer()
		if len(node.fields) != 0 {
			encodedBytes++
		}
		encodedBytes += field.keyBytes + 1 + value.bytes
		node.fields = append(node.fields, jsonOutputField{field.key, value.output})
	}
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (plan *compiledOutputPlan) buildArray(state evaluation) (plannedOutput, error) {
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(plan.items))}
	logicalBytes, encodedBytes := 0, 2
	for _, item := range plan.items {
		value, err := item.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		// Omitted operands still count before array construction, just as they
		// do in evaluateOperands; this is not a physical output-size check.
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		if !value.output.IsZero() {
			encodedBytes += appendPlannedItem(node, value)
		}
	}
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (plan *compiledOutputPlan) buildIteration(state evaluation) (plannedOutput, error) {
	expression := plan.expression
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	// Iteration owns its containers but borrows raw items until final encoding.
	// Returned Value storage is still independent of these temporary views.
	reader := &valueReader{}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	logicalBytes, encodedBytes := 0, 2
	for index, item := range items {
		child := state
		child.itemIndex = index
		child.item = item.value
		child.itemOutput = nil
		if item.node != nil {
			child.itemOutput = &item
		}
		value, err := plan.body.build(child)
		if err != nil {
			return plannedOutput{}, err
		}
		if value.output.IsZero() {
			continue
		}
		if expression.op == "flatmap" {
			if value.output.node != nil && !value.output.node.object {
				if len(value.output.node.items) != 0 {
					encodedBytes += value.bytes - 2
					if len(node.items) != 0 {
						encodedBytes++
					}
					node.items = append(node.items, value.output.node.items...)
				}
			} else {
				if value.output.node != nil || !value.output.value.IsArray() {
					return plannedOutput{}, fmt.Errorf("expected an array")
				}
				values, err := value.output.value.readArrayWithin(reader)
				if err != nil {
					return plannedOutput{}, err
				}
				for _, element := range values {
					encodedBytes += appendPlannedItem(node, plannedOutput{JSONLeaf(element), element.Len()})
				}
			}
		} else {
			encodedBytes += appendPlannedItem(node, value)
		}
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		if len(node.items) > state.budget.limits.Nodes {
			return plannedOutput{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, len(node.items))
		}
	}
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (value plannedOutput) normalizeForContainer() plannedOutput {
	if value.output.node == nil {
		value.output.value.raw = compactValueString(value.output.value.raw)
		value.bytes = value.output.value.Len()
	}
	return value
}

func appendPlannedItem(node *jsonOutputNode, value plannedOutput) int {
	value = value.normalizeForContainer()
	bytes := value.bytes
	if len(node.items) != 0 {
		bytes++
	}
	node.items = append(node.items, value.output)
	return bytes
}
