package protocol

import (
	"context"
	"fmt"
)

// compiledOutputPlan keeps construction nodes unencoded until the mapping
// returns. Eligibility is decided before execution; a failed plan is never rerun.
type compiledOutputPlan struct {
	expression *compiledExpression
	fields     []outputPlanField
	items      []*compiledOutputPlan
	body       *compiledOutputPlan
	then       *compiledOutputPlan
	otherwise  *compiledOutputPlan
	cases      map[string]*compiledOutputPlan
	isLeaf     bool
}

type outputPlanField struct {
	key        string
	keyBytes   int
	expression *compiledOutputPlan
}

func compileOutputPlan(expression *compiledExpression) (*compiledOutputPlan, bool) {
	if expression == nil {
		return nil, true
	}
	plan := &compiledOutputPlan{expression: expression}
	if isOutputLeafExpression(expression) {
		plan.isLeaf = true
		return plan, true
	}
	switch expression.op {
	case "object":
		for _, key := range expression.keys {
			child, ok := compileOutputPlan(expression.fields[key])
			if !ok {
				return nil, false
			}
			plan.fields = append(plan.fields, outputPlanField{key, jsonStringBytes(key), child})
		}
	case "array", "merge", "concat":
		for _, item := range expression.items {
			child, ok := compileOutputPlan(item)
			if !ok {
				return nil, false
			}
			plan.items = append(plan.items, child)
		}
	case "map", "flatmap", "filter":
		if !isOutputLeafExpression(expression.source) {
			return nil, false
		}
		if expression.op == "filter" {
			if !isOutputLeafExpression(expression.when) {
				return nil, false
			}
			return plan, true
		}
		var ok bool
		plan.body, ok = compileOutputPlan(expression.body)
		if !ok {
			return nil, false
		}
	case "if", "choose":
		condition := expression.when
		if expression.op == "choose" {
			condition = expression.source
		}
		if !isOutputLeafExpression(condition) {
			return nil, false
		}
		var ok bool
		plan.then, ok = compileOutputPlan(expression.then)
		if !ok {
			return nil, false
		}
		plan.otherwise, ok = compileOutputPlan(expression.otherwise)
		if !ok {
			return nil, false
		}
		plan.cases = make(map[string]*compiledOutputPlan, len(expression.cases))
		for key, branch := range expression.cases {
			child, ok := compileOutputPlan(branch)
			if !ok {
				return nil, false
			}
			plan.cases[key] = child
		}
	default:
		return nil, false
	}
	return plan, true
}

// Scalar operations retain their evaluator, including exact numbers and error
// priority. No container constructor may hide beneath a leaf operation.
func isOutputLeafExpression(expression *compiledExpression) bool {
	if expression == nil {
		return false
	}
	switch expression.op {
	case "omit", "literal", "read":
		return true
	case "exists", "present", "not", "enum", "cast", "strip_prefix", "trim_prefix":
		return isOutputLeafExpression(expression.source)
	case "equal", "all", "any":
		for _, item := range expression.items {
			if !isOutputLeafExpression(item) {
				return false
			}
		}
		return true
	}
	return false
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
	// Cancellation belongs to the same expression boundaries as the original
	// evaluator. Final materialization must not change its error precedence.
	return value.output.Encode(context.WithoutCancel(state.ctx))
}

func (plan *compiledOutputPlan) build(state evaluation) (value plannedOutput, err error) {
	if plan.isLeaf {
		leaf, err := plan.expression.evaluate(state)
		return plannedOutput{JSONLeaf(leaf), leaf.Len()}, err
	}
	expression := plan.expression
	defer func() { err = locateMappingError(expression.path, err) }()
	state.budget.steps++
	stepLimit := state.budget.stepLimit
	if stepLimit == 0 {
		stepLimit = state.budget.limits.Nodes
	}
	if state.budget.steps > stepLimit {
		return plannedOutput{}, resourceFailure("evaluation", "steps", stepLimit, state.budget.steps)
	}
	if err := state.ctx.Err(); err != nil {
		return plannedOutput{}, err
	}
	defer func() {
		if err == nil && value.bytes > state.budget.limits.BufferBytes {
			err = resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, value.bytes)
			value = plannedOutput{}
		}
	}()
	switch expression.op {
	case "object":
		return plan.buildObject(state)
	case "array":
		return plan.buildArray(state)
	case "merge", "concat":
		return plan.buildOperands(state)
	case "map", "flatmap":
		return plan.buildIteration(state)
	case "filter":
		return plan.buildFilter(state)
	case "if":
		condition, err := expression.when.evaluate(state)
		if err != nil {
			return plannedOutput{}, err
		}
		isTrue, err := readBoolean(condition)
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
		discriminator, err := expression.source.evaluate(state)
		if err != nil {
			return plannedOutput{}, err
		}
		key, err := readString(discriminator)
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
	source, err := expression.source.evaluate(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if !source.IsArray() {
		return plannedOutput{}, fmt.Errorf("%s/source: expected an array", expression.path)
	}
	// Iteration owns its containers but borrows raw items until final encoding.
	// Returned Value storage is still independent of these temporary views.
	reader := &valueReader{}
	items, err := source.readArrayWithin(reader)
	if err != nil {
		return plannedOutput{}, err
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	logicalBytes, encodedBytes := 0, 2
	for _, item := range items {
		child := state
		child.item = item
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
