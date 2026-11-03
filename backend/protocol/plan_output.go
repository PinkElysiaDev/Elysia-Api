package protocol

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// compiledOutputPlan keeps construction nodes unencoded until the mapping
// returns. Eligibility is decided before execution; a failed plan is never rerun.
type compiledOutputPlan struct {
	hasCanonicalFields     bool
	hasStreamWrites        bool
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
	hasSourceCapture       bool
	hasNativeTarget        bool
	hasOutputStreamRead    bool
	hasEncodingStateRead   bool
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
	if expression.outputPlan != nil {
		return expression.outputPlan, true
	}
	plan := &compiledOutputPlan{expression: expression}
	switch expression.op {
	case "omit", "reject", "literal", "read", "let", "with_input", "with_index", "remember", "recall", "forget", "index_size", "index", "capture_native", "capture_source", "is_carrier", "replay_native", "native_matches", "object", "check_fields", "project", "keys", "array", "map", "walk", "visit", "recur", "flatmap", "filter", "if", "choose",
		"merge", "merge_deep", "merge_usage", "check_usage", "check_links", "resolve_item", "track_text", "track_array", "track_position", "project_range", "require_disposition", "json_digest", "track_tool", "buffer_text", "buffer_array", "read_delivered_item", "resume_text", "resume_tool", "pointer_join", "index_values", "length", "concat", "join", "sort", "associate", "parse_json", "stringify_json", "split_data_uri",
		"exists", "present", "type", "is_integer", "integer", "not", "enum", "cast", "strip_prefix", "trim_prefix", "lower", "quote_string", "has_prefix", "contains", "has_control", "equal", "equal_text", "all", "any", "entries", "from_entries", "group_runs", "utf16_length":
	default:
		return nil, false
	}
	if expression.op == "let" || expression.op == "read" && expression.from == "binding" {
		plan.bindingSlots = expression.bindingSlot + 1
	}
	if !expression.isStreamIndex && (expression.op == "with_index" || expression.op == "remember" || expression.op == "recall" || expression.op == "forget" || expression.op == "index_size") {
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
	plan.hasStreamWrites = expression.op == "require_disposition" || expression.op == "resolve_item" || expression.op == "track_text" || expression.op == "track_array" || expression.op == "track_position" || expression.op == "track_tool" || expression.op == "buffer_text" || expression.op == "buffer_array" || expression.op == "resume_text" || expression.op == "resume_tool" || expression.op == "remember" && expression.isStreamIndex
	plan.hasCapture = expression.op == "capture_native"
	plan.hasSourceCapture = expression.op == "capture_source" || expression.op == "is_carrier"
	plan.hasNativeTarget = expression.op == "replay_native" || expression.op == "native_matches"
	plan.hasOutputStreamRead = expression.op == "read" && expression.from == "runtime" && len(expression.pointer) == 2 && (expression.pointer[0] == "stream" || expression.pointer[0] == "emission")
	plan.hasEncodingStateRead = expression.op == "read" && expression.from == "runtime" && len(expression.pointer) == 2 && expression.pointer[0] == "encoding"
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
		{expression.body, &plan.body, expression.op != "map" && expression.op != "flatmap" && expression.op != "walk" && expression.op != "visit"},
		{expression.when, &plan.when, expression.op != "filter" && expression.op != "group_runs"},
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
		plan.hasCanonicalFields = true
		for _, key := range expression.keys {
			plan.hasCanonicalFields = plan.hasCanonicalFields && utf8.ValidString(key)
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
	expression.outputPlan = plan
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
	plan.hasStreamWrites = plan.hasStreamWrites || child.hasStreamWrites
	plan.hasCapture = plan.hasCapture || child.hasCapture
	plan.hasSourceCapture = plan.hasSourceCapture || child.hasSourceCapture
	plan.hasNativeTarget = plan.hasNativeTarget || child.hasNativeTarget
	plan.hasOutputStreamRead = plan.hasOutputStreamRead || child.hasOutputStreamRead
	plan.hasEncodingStateRead = plan.hasEncodingStateRead || child.hasEncodingStateRead
}

type plannedOutput struct {
	output JSONOutput
	bytes  int
}

func (plan *compiledOutputPlan) evaluate(state evaluation) (Value, error) {
	value, err := plan.build(state)
	if err != nil {
		return Value{}, err
	}
	return value.materializeWithin(state.ctx, state.cache)
}

func (value plannedOutput) materialize(ctx context.Context) (Value, error) {
	return value.materializeWithin(ctx, nil)
}

func (value plannedOutput) materializeWithin(ctx context.Context, cache *semanticCache) (Value, error) {
	if value.output.node == nil {
		return value.output.value, nil
	}
	// Cancellation belongs to the same expression boundaries as the original
	// evaluator. Final materialization must not change its error precedence.
	return value.output.encodeWithin(context.WithoutCancel(ctx), cache)
}

func (plan *compiledOutputPlan) build(state evaluation) (value plannedOutput, err error) {
	if plan.bindingSlots != 0 && state.bindings == nil {
		state.bindings = make([]JSONOutput, plan.bindingSlots)
		if state.capture != nil {
			state.capture.bindings = make([]nativeLocation, plan.bindingSlots)
		}
	}
	return plan.buildNode(state)
}

func (plan *compiledOutputPlan) buildNode(state evaluation) (value plannedOutput, err error) {
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
	case "with_input":
		return plan.buildInputScope(state)
	case "check_usage":
		return plan.checkUsage(state)
	case "check_links":
		return plan.checkLinks(state)
	case "pointer_join":
		return plan.buildPointerJoin(state)
	case "resolve_item":
		return plan.resolveStreamItem(state)
	case "track_text":
		return plan.trackStreamText(state)
	case "track_array":
		return plan.trackStreamArray(state)
	case "track_position":
		return plan.trackStreamPosition(state)
	case "project_range":
		return plan.projectStreamRange(state)
	case "require_disposition":
		return plan.requireStreamDisposition(state)
	case "json_digest":
		return plan.buildJSONDigest(state)
	case "track_tool":
		return plan.trackStreamTool(state)
	case "buffer_text", "buffer_array":
		return plan.useStreamBuffer(state)
	case "read_delivered_item":
		return plan.readDeliveredItem(state)
	case "resume_text":
		return plan.resumeDeliveredText(state)
	case "resume_tool":
		return plan.resumeDeliveredTool(state)
	case "index_values":
		return plan.readStreamIndexValues(state)
	case "with_index":
		return plan.buildIndexScope(state)
	case "remember":
		return plan.rememberValue(state)
	case "recall":
		return plan.recallValue(state)
	case "forget":
		return plan.forgetValue(state)
	case "index_size":
		if expression.isStreamIndex {
			return plannedLeaf(coreInteger(int64(state.streamTransaction.indexSize(expression.indexSlot)))), nil
		}
		return plannedLeaf(coreInteger(int64(len(state.indexes.frames[expression.indexSlot].values)))), nil
	case "index":
		return plannedLeaf(coreInteger(int64(state.itemIndex))), nil
	case "capture_native":
		return plan.captureNative(state)
	case "capture_source":
		return plan.captureSource(state)
	case "is_carrier":
		return plan.isCarrier(state)
	case "replay_native", "native_matches":
		return plan.buildNativeTarget(state)
	case "object":
		return plan.buildObject(state)
	case "reject":
		return plannedOutput{}, plan.reject(state)
	case "check_fields":
		return plan.checkObjectFields(state)
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
	case "length":
		return plan.countArrayMembers(state)
	case "merge", "concat":
		return plan.buildOperands(state)
	case "merge_deep":
		return plan.buildDeepMerge(state)
	case "merge_usage":
		return plan.mergeUsage(state)
	case "join":
		return plan.buildJoin(state)
	case "sort", "associate":
		return plan.buildKeyedArray(state)
	case "parse_json", "stringify_json":
		return plan.buildJSONScalar(state)
	case "exists", "present", "not", "enum", "cast", "strip_prefix", "trim_prefix", "split_data_uri", "lower", "quote_string", "has_prefix", "contains", "has_control", "utf16_length":
		return plan.buildScalar(state)
	case "type", "is_integer":
		return plan.buildTypeQuery(state)
	case "integer":
		return plan.buildInteger(state)
	case "equal", "all", "any":
		return plan.buildScalarOperands(state)
	case "equal_text":
		return plan.compareText(state)
	case "map", "flatmap":
		return plan.buildIteration(state)
	case "walk", "recur":
		return plan.buildWalk(state)
	case "visit":
		return plan.buildVisit(state)
	case "filter":
		return plan.buildFilter(state)
	case "group_runs":
		return plan.buildRuns(state)
	case "if":
		condition, err := plan.when.buildNode(state)
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
		return branch.buildNode(state)
	case "choose":
		discriminator, err := plan.source.buildNode(state)
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
		return branch.buildNode(state)
	}
	panic("uncompiled output operation")
}

func (plan *compiledOutputPlan) buildObject(state evaluation) (plannedOutput, error) {
	node := &jsonOutputNode{object: true, hasCanonicalKeys: plan.hasCanonicalFields, hasSortedFields: true, fields: make([]jsonOutputField, 0, len(plan.fields))}
	logicalBytes, encodedBytes := 0, 2
	for _, field := range plan.fields {
		value, err := field.expression.buildNode(state)
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
		value = value.normalizeForContainer(state.cache)
		if len(node.fields) != 0 {
			encodedBytes++
		}
		encodedBytes += field.keyBytes + 1 + value.bytes
		node.fields = append(node.fields, jsonOutputField{field.key, value.output})
	}
	node.encodedBytes = encodedBytes
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (plan *compiledOutputPlan) buildArray(state evaluation) (plannedOutput, error) {
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(plan.items))}
	logicalBytes, encodedBytes := 0, 2
	for _, item := range plan.items {
		value, err := item.buildNode(state)
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
			encodedBytes += appendPlannedItem(node, value, state.cache)
		}
	}
	node.encodedBytes = encodedBytes
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (plan *compiledOutputPlan) buildIteration(state evaluation) (plannedOutput, error) {
	source, err := plan.source.buildNode(state)
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
		value, err := body.buildNode(child)
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
					encodedBytes += appendPlannedItem(node, plannedOutput{JSONLeaf(element), element.Len()}, state.cache)
				}
			}
		} else {
			encodedBytes += appendPlannedItem(node, value, state.cache)
		}
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		if len(node.items) > state.budget.limits.Nodes {
			return plannedOutput{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, len(node.items))
		}
	}
	node.encodedBytes = encodedBytes
	return plannedOutput{JSONOutput{node: node}, encodedBytes}, nil
}

func (value plannedOutput) normalizeForContainer(cache *semanticCache) plannedOutput {
	if value.output.node == nil {
		value.output.value = cache.normalize(value.output.value)
		value.bytes = value.output.value.Len()
	}
	return value
}

func appendPlannedItem(node *jsonOutputNode, value plannedOutput, cache *semanticCache) int {
	value = value.normalizeForContainer(cache)
	bytes := value.bytes
	if len(node.items) != 0 {
		bytes++
	}
	node.items = append(node.items, value.output)
	return bytes
}
