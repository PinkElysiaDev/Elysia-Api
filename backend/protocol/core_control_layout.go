package protocol

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
)

// ProtocolContentLayout rearranges existing root nodes without replacing their
// payloads or granting provenance. Combining children requires plain containers;
// local steps may change the selected role.
type ProtocolContentLayout struct {
	Operation    string                `json:"operation" jsonschema:"enum=stable_partition,enum=combine_children,enum=group_runs"`
	Path         string                `json:"path" jsonschema:"enum=/content"`
	Role         string                `json:"role,omitempty"`
	When         Expression            `json:"when"`
	PathBinding  string                `json:"pathBinding,omitempty"`
	MovedBinding string                `json:"movedBinding,omitempty"`
	Steps        []ProtocolControlStep `json:"steps,omitempty"`
}

type compiledContentLayout struct {
	operation           string
	role                Value
	when                *compiledOutputPlan
	steps               []compiledControlStep
	pathSlot, movedSlot int
	bindingSlots        int
}

func compileContentLayout(declaration ProtocolContentLayout, compiler *expressionCompiler, path string, outer controlCompileScope, count *int) (*compiledContentLayout, error) {
	if !slices.Contains([]string{"stable_partition", "combine_children", "group_runs"}, declaration.Operation) || declaration.Path != "/content" || outer.whole != reflect.TypeFor[Request]() && outer.whole != reflect.TypeFor[Response]() {
		return nil, fmt.Errorf("%s: content layout requires request or complete response content", path)
	}
	if (declaration.Operation == "group_runs") != (declaration.Role != "") {
		return nil, fmt.Errorf("%s/role: only group_runs requires a container role", path)
	}
	local := controlCompileScope{whole: outer.whole, isLayout: true, expression: outer.expression}
	local.expression.coreRoot, local.expression.coreInput = outer.whole, reflect.TypeFor[Node]()
	local.expression.hasInputScope = true
	plan := &compiledContentLayout{operation: declaration.Operation, pathSlot: -1, movedSlot: -1}
	if declaration.Role != "" {
		plan.role = StringValue(declaration.Role)
	}
	local.expression.bindings = maps.Clone(outer.expression.bindings)
	if local.expression.bindings == nil {
		local.expression.bindings = make(map[string]expressionBinding)
	}
	for _, binding := range []struct {
		name string
		kind JSONType
		slot *int
	}{{declaration.PathBinding, StringType, &plan.pathSlot}, {declaration.MovedBinding, BooleanType, &plan.movedSlot}} {
		if binding.name == "" {
			continue
		}
		if !definitionIdentifier.MatchString(binding.name) {
			return nil, fmt.Errorf("%s: layout bindings require identifiers", path)
		}
		if _, exists := local.expression.bindings[binding.name]; exists {
			return nil, fmt.Errorf("%s: layout binding %s is already declared", path, binding.name)
		}
		*binding.slot = local.expression.bindingDepth
		local.expression.bindingDepth++
		local.expression.bindings[binding.name] = expressionBinding{slot: *binding.slot, result: binding.kind}
	}
	var err error
	if declaration.Operation == "combine_children" || declaration.Operation == "group_runs" {
		if len(declaration.Steps) == 0 || declaration.Steps[0].When != nil || declaration.Steps[0].Diagnostic == nil || (declaration.Steps[0].Diagnostic.Kind != "degrade" && declaration.Steps[0].Diagnostic.Kind != "reject") {
			return nil, fmt.Errorf("%s/steps: container layout requires an unconditional boundary-loss diagnostic first", path)
		}
		for _, step := range declaration.Steps {
			if len(step.Set) != 0 {
				return nil, fmt.Errorf("%s/steps: container layout cannot also change container fields", path)
			}
		}
	}
	plan.when, err = compiler.compilePlan(declaration.When, path+"/when", local.expression, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.when.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s/when: layout selection must produce a boolean", path)
	}
	plan.steps, err = compileScopedControlSteps(declaration.Steps, compiler, path+"/steps", false, local, count)
	plan.bindingSlots = max(local.expression.bindingDepth, plan.when.bindingSlots, controlBindingSlots(plan.steps))
	return plan, err
}

func (plan *compiledContentLayout) apply(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	var content *[]Node
	var native **Native
	switch current := document.(type) {
	case *Request:
		content, native = &current.Content, &current.Native
	case *Response:
		content, native = &current.Content, &current.Native
	default:
		panic("compiled content layout received a different core type")
	}
	if plan.operation == "group_runs" {
		return plan.groupRuns(state, conversion, content, native, route, rule, sink, owner)
	}
	input := *content
	var selected []bool
	var combined []Node
	firstSelected := -1
	firstUnselected, selectedCount := -1, 0
	hasChanges := false
	for index := range input {
		if err := state.advance(); err != nil {
			return false, err
		}
		node := &input[index]
		isMoved := index != selectedCount
		local := plan.local(state, node, index, isMoved)
		value, err := plan.when.evaluate(local)
		if err != nil {
			return false, err
		}
		isSelected, err := readBoolean(value)
		if err != nil {
			return false, err
		}
		if !isSelected {
			if firstUnselected < 0 {
				firstUnselected = index
			}
			continue
		}
		if plan.operation == "combine_children" {
			if !isPlainContentContainer(*node) {
				return false, streamIssue(UnsupportedCapability, "/content/"+strconv.Itoa(index), "combining children requires an explicit representation of container fields")
			}
			if firstSelected < 0 {
				firstSelected = index
			} else {
				if index != firstSelected+selectedCount {
					return false, streamIssue(UnsupportedCapability, "/content/"+strconv.Itoa(index), "combining children cannot cross an unselected root")
				}
				if node.Kind != input[firstSelected].Kind || !equalValues(node.Role, input[firstSelected].Role) {
					return false, streamIssue(UnsupportedCapability, "/content/"+strconv.Itoa(index), "combining children requires equal container kind and role")
				}
				if selected == nil {
					selected = make([]bool, len(input))
					combined = slices.Clone(input[firstSelected].Children)
				}
				selected[index] = true
				combined = append(combined, node.Children...)
			}
		}
		selectedCount++
		if plan.operation == "combine_children" && selectedCount == 1 {
			continue
		}
		result, err := applyControlSteps(local, conversion, node, route, rule, sink, plan.steps, owner)
		if err != nil {
			return false, err
		}
		if result.hasChanges {
			node.Native = nil
			hasChanges = true
		}
		if plan.operation == "stable_partition" && firstUnselected >= 0 {
			if selected == nil {
				selected = make([]bool, len(input))
			}
			selected[index] = true
		}
	}
	if selected != nil {
		if plan.operation == "stable_partition" {
			*content = partitionContent(input, selected, firstUnselected)
		} else {
			input[firstSelected].Children, input[firstSelected].Native = combined, nil
			output := make([]Node, 0, len(input)-selectedCount+1)
			for index := range input {
				if !selected[index] {
					output = append(output, input[index])
				}
			}
			*content = output
		}
		hasChanges = true
	}
	if hasChanges {
		*native = nil
	}
	return hasChanges, nil
}

func (plan *compiledContentLayout) local(state evaluation, node *Node, index int, isMoved bool) evaluation {
	state.input, state.objectReader = Value{}, nil
	state.inputReader = func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer) }
	if plan.pathSlot >= 0 {
		state.bindings[plan.pathSlot] = JSONLeaf(StringValue("/content/" + strconv.Itoa(index)))
	}
	if plan.movedSlot >= 0 {
		state.bindings[plan.movedSlot] = JSONLeaf(coreBoolean(isMoved))
	}
	return state
}

func isPlainContentContainer(node Node) bool {
	return node.ID.IsZero() && node.CallID.IsZero() && node.Name.IsZero() && node.Status.IsZero() &&
		node.Payload.IsZero() && node.Input == nil && node.ReasoningForm == "" && node.ReasoningContent == nil &&
		len(node.Metadata) == 0 && len(node.Cache) == 0 && len(node.Resources) == 0 && len(node.Attributes) == 0
}

func partitionContent(input []Node, selected []bool, start int) []Node {
	output := make([]Node, len(input))
	copy(output, input[:start])
	position := start
	for _, isSelected := range []bool{true, false} {
		for index := start; index < len(input); index++ {
			if selected[index] == isSelected {
				output[position] = input[index]
				position++
			}
		}
	}
	return output
}
