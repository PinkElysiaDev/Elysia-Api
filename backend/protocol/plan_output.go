package protocol

import (
	"context"
	"fmt"
)

// compiledOutputPlan keeps construction nodes unencoded until the mapping
// returns. Eligibility is decided before execution; a failed plan is never rerun.
type compiledOutputPlan struct {
	expression             *compiledExpression
	fields                 []outputPlanField
	items                  []*compiledOutputPlan
	source                 *compiledOutputPlan
	body                   *compiledOutputPlan
	when                   *compiledOutputPlan
	then                   *compiledOutputPlan
	otherwise              *compiledOutputPlan
	cases                  map[string]*compiledOutputPlan
	bindingSlots           int
	indexSlots             int
	rootObjectReads        uint8
	itemObjectReads        uint8
	hasCapture             bool
	hasNativeTarget        bool
	hasOutputStreamRead    bool
	hasRequestObjectSource bool
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
	case "omit", "literal", "read", "let", "with_index", "remember", "recall", "index", "capture_native", "replay_native", "native_matches", "object", "project", "keys", "array", "map", "walk", "recur", "flatmap", "filter", "if", "choose",
		"merge", "merge_deep", "concat", "join", "sort", "associate", "parse_json", "stringify_json", "split_data_uri",
		"exists", "present", "type", "is_integer", "integer", "not", "enum", "cast", "strip_prefix", "trim_prefix", "equal", "all", "any", "entries", "from_entries":
	default:
		return nil, false
	}
	if expression.op == "let" || expression.op == "read" && expression.from == "binding" {
		plan.bindingSlots = expression.bindingSlot + 1
	}
	if expression.op == "with_index" || expression.op == "remember" || expression.op == "recall" {
		plan.indexSlots = expression.indexSlot + 1
	}
	if expression.op == "read" && len(expression.pointer) != 0 {
		switch expression.from {
		case "input", "root":
			plan.rootObjectReads = 1
		case "item":
			plan.itemObjectReads = 1
		}
	}
	plan.hasCapture = expression.op == "capture_native"
	plan.hasNativeTarget = expression.op == "replay_native" || expression.op == "native_matches"
	plan.hasOutputStreamRead = expression.op == "read" && expression.from == "runtime" && len(expression.pointer) == 2 && expression.pointer[0] == "stream"
	if (expression.op == "keys" || expression.op == "type") && expression.source != nil && expression.source.op == "read" && (expression.source.from == "input" || expression.source.from == "root") {
		plan.hasRequestObjectSource = isCoreRequestPointerObjectPath(expression.source.pointer)
	}
	// Nested iteration bodies bind a different item; their reads cannot justify
	// retaining the caller's item parse tree.
	for _, child := range []struct {
		source      *compiledExpression
		target      **compiledOutputPlan
		hasSameItem bool
	}{
		{expression.source, &plan.source, true},
		{expression.body, &plan.body, expression.op != "map" && expression.op != "flatmap" && expression.op != "walk"},
		{expression.when, &plan.when, expression.op != "filter"},
		{expression.then, &plan.then, true},
		{expression.otherwise, &plan.otherwise, true},
	} {
		compiled, ok := compileOutputPlan(child.source)
		if !ok {
			return nil, false
		}
		*child.target = compiled
		plan.mergeRequirements(compiled, child.hasSameItem)
	}
	if expression.op == "object" {
		for _, key := range expression.keys {
			child, ok := compileOutputPlan(expression.fields[key])
			if !ok {
				return nil, false
			}
			plan.fields = append(plan.fields, outputPlanField{key, jsonStringBytes(key), child})
			plan.mergeRequirements(child, true)
		}
	}
	for _, item := range expression.items {
		child, ok := compileOutputPlan(item)
		if !ok {
			return nil, false
		}
		plan.items = append(plan.items, child)
		plan.mergeRequirements(child, true)
	}
	if len(expression.cases) != 0 {
		plan.cases = make(map[string]*compiledOutputPlan, len(expression.cases))
		for key, branch := range expression.cases {
			child, ok := compileOutputPlan(branch)
			if !ok {
				return nil, false
			}
			plan.cases[key] = child
			plan.mergeRequirements(child, true)
		}
	}
	return plan, true
}

func (plan *compiledOutputPlan) mergeRequirements(child *compiledOutputPlan, hasSameItem bool) {
	if child == nil {
		return
	}
	plan.bindingSlots = max(plan.bindingSlots, child.bindingSlots)
	plan.indexSlots = max(plan.indexSlots, child.indexSlots)
	plan.rootObjectReads = min(objectReuseReads, plan.rootObjectReads+child.rootObjectReads)
	if hasSameItem {
		plan.itemObjectReads = min(objectReuseReads, plan.itemObjectReads+child.itemObjectReads)
	}
	plan.hasCapture = plan.hasCapture || child.hasCapture
	plan.hasNativeTarget = plan.hasNativeTarget || child.hasNativeTarget
	plan.hasOutputStreamRead = plan.hasOutputStreamRead || child.hasOutputStreamRead
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
		if state.capture != nil {
			state.capture.bindings = make([]nativeLocation, plan.bindingSlots)
		}
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
	case "with_index":
		return plan.buildIndexScope(state)
	case "remember":
		return plan.rememberValue(state)
	case "recall":
		return plan.recallValue(state)
	case "index":
		return plannedLeaf(coreInteger(int64(state.itemIndex))), nil
	case "capture_native":
		return plan.captureNative(state)
	case "replay_native", "native_matches":
		return plan.buildNativeTarget(state)
	case "object":
		return plan.buildObject(state)
	case "project":
		return plan.buildProjection(state)
	case "keys":
		return plan.buildKeys(state)
	case "entries":
		return plan.buildEntries(state)
	case "from_entries":
		return plan.buildObjectEntries(state)
	case "array":
		return plan.buildArray(state)
	case "merge", "concat":
		return plan.buildOperands(state)
	case "merge_deep":
		return plan.buildDeepMerge(state)
	case "join":
		return plan.buildJoin(state)
	case "sort", "associate":
		return plan.buildKeyedArray(state)
	case "parse_json", "stringify_json":
		return plan.buildJSONScalar(state)
	case "exists", "present", "not", "enum", "cast", "strip_prefix", "trim_prefix", "split_data_uri":
		return plan.buildScalar(state)
	case "type", "is_integer":
		return plan.buildTypeQuery(state)
	case "integer":
		return plan.buildInteger(state)
	case "equal", "all", "any":
		return plan.buildScalarOperands(state)
	case "map", "flatmap":
		return plan.buildIteration(state)
	case "walk", "recur":
		return plan.buildWalk(state)
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
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	return plan.buildIterationOutput(state, source, plan.body)
}

func (plan *compiledOutputPlan) buildIterationOutput(state evaluation, source plannedOutput, body *compiledOutputPlan) (plannedOutput, error) {
	expression := plan.expression
	// Iteration owns its containers but borrows raw items until final encoding.
	// Returned Value storage is still independent of these temporary views.
	reader := &valueReader{}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	logicalBytes, encodedBytes := 0, 2
	isWalk := expression.op == "walk" || expression.op == "recur"
	var itemReader *planObjectReader
	for index, item := range items {
		if isWalk {
			state.budget.walkNodes++
			if state.budget.walkNodes > state.budget.limits.Nodes {
				return plannedOutput{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, state.budget.walkNodes)
			}
		}
		child := state
		if state.capture != nil {
			child.capture = state.capture.forItem(plan.source.expression, index)
		}
		child.itemIndex = index
		child.item = item.value
		child.itemOutput = nil
		if item.node != nil {
			child.itemOutput = &item
		} else if body.itemObjectReads >= objectReuseReads && item.value.IsObject() {
			if itemReader == nil {
				itemReader = new(planObjectReader)
			}
			*itemReader = planObjectReader{root: spanIdentity(item.value.raw), parent: state.objectReader}
			child.objectReader = itemReader
		}
		value, err := body.build(child)
		itemReader.releaseItem(err)
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
