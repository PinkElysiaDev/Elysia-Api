package protocol

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

type compiledAggregateLayout struct {
	remainder string
	fields    []aggregateField
	labels    []aggregateLabel
	members   map[string]bool
	nextOwner []string
}

type aggregateField struct{ wire, core string }
type aggregateLabel struct {
	name  string
	value Value
}

func compilePackageAggregateLayout(direction Direction, core []string, layout *ProtocolAggregateLayout) (*compiledAggregateLayout, error) {
	if layout == nil || (direction != DecodeRequest && direction != EncodeRequest) || len(core) == 0 || core[0] == "schemaVersion" || isProtectedAggregateField(core[0]) {
		return nil, fmt.Errorf("aggregate requires a request semantic owner and an explicit layout")
	}
	owner := aggregateOwnerType(reflect.TypeFor[Request](), core)
	if owner == nil || owner.Kind() != reflect.Struct || owner == reflect.TypeFor[Value]() {
		return nil, fmt.Errorf("aggregate owner requires a declared core object")
	}
	compiled := &compiledAggregateLayout{remainder: layout.Remainder, members: make(map[string]bool)}
	if index, err := strconv.Atoi(core[len(core)-1]); err == nil {
		compiled.nextOwner = slices.Clone(core)
		compiled.nextOwner[len(core)-1] = strconv.Itoa(index + 1)
	}
	members := make(map[string]reflect.Type)
	for index := range owner.NumField() {
		field := owner.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.IsExported() && name != "" && name != "-" {
			members[name] = field.Type
			compiled.members[name] = false
		}
	}
	if members[layout.Remainder] != reflect.TypeFor[Value]() {
		return nil, fmt.Errorf("aggregate remainder requires a core Value field")
	}
	compiled.members[layout.Remainder] = true
	seen := map[string]bool{layout.Remainder: true}
	for _, wire := range sortedKeys(layout.Fields) {
		member := layout.Fields[wire]
		if wire == "" || seen[member] || members[member] != reflect.TypeFor[Value]() {
			return nil, fmt.Errorf("aggregate fields require distinct declared Value members")
		}
		seen[member] = true
		compiled.members[member] = true
		compiled.fields = append(compiled.fields, aggregateField{wire: wire, core: member})
	}
	for _, name := range sortedKeys(layout.Labels) {
		label := layout.Labels[name]
		kind := members[name]
		if seen[name] || kind == nil || label.IsZero() || label.IsNull() || !slices.Contains([]reflect.Kind{reflect.String, reflect.Bool, reflect.Int, reflect.Int64}, kind.Kind()) {
			return nil, fmt.Errorf("aggregate labels require distinct declared scalar members")
		}
		if err := label.Decode(reflect.New(kind).Interface()); err != nil {
			return nil, fmt.Errorf("aggregate label conflicts with its core type: %w", err)
		}
		seen[name] = true
		compiled.members[name] = true
		compiled.labels = append(compiled.labels, aggregateLabel{name: name, value: label})
	}
	return compiled, nil
}

func aggregateOwnerType(root reflect.Type, pointer []string) reflect.Type {
	for _, key := range pointer {
		for root.Kind() == reflect.Pointer {
			root = root.Elem()
		}
		switch root.Kind() {
		case reflect.Slice:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || strconv.Itoa(index) != key {
				return nil
			}
			root = root.Elem()
		case reflect.Struct:
			var selected reflect.Type
			for index := range root.NumField() {
				field := root.Field(index)
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if field.IsExported() && name == key && !isProtectedAggregateField(name) {
					selected = field.Type
					break
				}
			}
			if selected == nil {
				return nil
			}
			root = selected
		default:
			return nil
		}
	}
	for root.Kind() == reflect.Pointer {
		root = root.Elem()
	}
	return root
}

func isProtectedAggregateField(name string) bool {
	switch name {
	case "source", "native", "resource", "resources", "clientOutput":
		return true
	}
	return false
}

func provesPackageAggregate(program *compiledExpression, direction Direction, effect compiledSemanticEffect, steps int) bool {
	input, output := effect.corePointer, effect.wirePointer
	if direction == DecodeRequest {
		input, output = output, input
	}
	if effect.when == nil || !aggregatePresence(effect.when.expression, "exists", input) {
		return false
	}
	proof := aggregateOutputProof{steps: steps}
	return proof.output(program, nil, output, input, direction, effect.layout, false)
}

// Presence follows from the effect precondition. Unrelated branches must each
// prove the same ownership; merges cannot contribute to an already owned field.
type aggregateOutputProof struct{ steps int }

func (proof *aggregateOutputProof) output(program *compiledExpression, scope *historyOwnerBinding, output, input []string, direction Direction, layout *ProtocolAggregateLayout, hasGuard bool) bool {
	if program == nil || proof.steps <= 0 {
		return false
	}
	proof.steps--
	if program.op == "let" {
		binding := historyOwnerBinding{slot: program.bindingSlot, source: program.source, sourceScope: scope, parent: scope}
		return proof.output(program.body, &binding, output, input, direction, layout, hasGuard)
	}
	if program.op == "read" && program.from == "binding" {
		for binding := scope; binding != nil; binding = binding.parent {
			if binding.slot == program.bindingSlot {
				return proof.output(binding.source, binding.sourceScope, append(slices.Clone(program.pointer), output...), input, direction, layout, hasGuard)
			}
		}
		return false
	}
	if program.op == "if" && aggregateKnownPresence(program.when, input) {
		return proof.output(program.then, scope, output, input, direction, layout, true)
	}
	if len(output) == 0 {
		if !hasGuard {
			return false
		}
		if direction == DecodeRequest {
			return provesObjectPartition(program, input, layout)
		}
		return provesObjectReconstruction(program, input, layout)
	}
	switch program.op {
	case "if":
		return proof.output(program.then, scope, output, input, direction, layout, hasGuard) &&
			proof.output(program.otherwise, scope, output, input, direction, layout, hasGuard)
	case "object":
		return proof.output(program.fields[output[0]], scope, output[1:], input, direction, layout, hasGuard)
	case "array":
		index, err := strconv.Atoi(output[0])
		if err != nil || index < 0 || index+1 != len(program.items) || strconv.Itoa(index) != output[0] {
			return false
		}
		for _, item := range program.items[:index] {
			if !alwaysPresentOutput(item) {
				return false
			}
		}
		return proof.output(program.items[index], scope, output[1:], input, direction, layout, hasGuard)
	case "concat":
		if output[0] != "0" || len(program.items) == 0 {
			return false
		}
		return proof.output(program.items[0], scope, output, input, direction, layout, hasGuard)
	case "merge", "merge_deep":
		if program.op == "merge" && program.policy != "reject" {
			return false
		}
		var owner *compiledExpression
		for _, item := range program.items {
			if proof.omitsKey(item, scope, output[0]) {
				continue
			}
			if owner != nil {
				return false
			}
			owner = item
		}
		return proof.output(owner, scope, output, input, direction, layout, hasGuard)
	}
	return false
}

func aggregateKnownPresence(condition *compiledExpression, input []string) bool {
	if aggregatePresence(condition, "exists", input) {
		return true
	}
	if condition != nil && condition.op == "any" {
		for _, item := range condition.items {
			if aggregatePresence(item, "exists", input) {
				return true
			}
		}
	}
	return false
}

func (proof *aggregateOutputProof) omitsKey(program *compiledExpression, scope *historyOwnerBinding, key string) bool {
	if program == nil || proof.steps <= 0 {
		return false
	}
	proof.steps--
	switch program.op {
	case "object":
		_, exists := program.fields[key]
		return !exists
	case "project":
		isListed := slices.Contains(program.keys, key)
		return (program.policy == "exclude" && isListed) || (program.policy == "include" && !isListed)
	case "if":
		return proof.omitsKey(program.then, scope, key) && proof.omitsKey(program.otherwise, scope, key)
	case "let":
		binding := historyOwnerBinding{slot: program.bindingSlot, source: program.source, sourceScope: scope, parent: scope}
		return proof.omitsKey(program.body, &binding, key)
	case "read":
		if program.from == "binding" {
			for binding := scope; binding != nil; binding = binding.parent {
				if binding.slot == program.bindingSlot && len(program.pointer) == 0 {
					return proof.omitsKey(binding.source, binding.sourceScope, key)
				}
			}
		}
	}
	return false
}

func provesObjectPartition(program *compiledExpression, input []string, layout *ProtocolAggregateLayout) bool {
	if program.op != "if" || !aggregatePresence(program.when, "present", input) ||
		!aggregateLabels(program.then, layout, len(layout.Fields)) || !aggregateLabels(program.otherwise, layout, 0) {
		return false
	}
	if !aggregateRead(program.otherwise.fields[layout.Remainder], input) {
		return false
	}
	remainder := program.then.fields[layout.Remainder]
	if remainder == nil || remainder.op != "project" || remainder.policy != "exclude" ||
		!aggregateRead(remainder.source, input) || !slices.Equal(remainder.keys, sortedKeys(layout.Fields)) {
		return false
	}
	for wire, core := range layout.Fields {
		field := program.then.fields[core]
		if !aggregateRead(field, aggregateMember(input, wire)) || field.isRequired {
			return false
		}
	}
	return true
}

func aggregateLabels(program *compiledExpression, layout *ProtocolAggregateLayout, parts int) bool {
	if program == nil || program.op != "object" || len(program.fields) != len(layout.Labels)+parts+1 {
		return false
	}
	for name, value := range layout.Labels {
		field := program.fields[name]
		if field == nil || field.op != "literal" || !equalValues(field.value, value) {
			return false
		}
	}
	return true
}

func provesObjectReconstruction(program *compiledExpression, input []string, layout *ProtocolAggregateLayout) bool {
	remainder := aggregateMember(input, layout.Remainder)
	if program.op != "if" || !aggregateNullTest(program.when, remainder) || !aggregateRead(program.then, remainder) {
		return false
	}
	merge := program.otherwise
	if merge == nil || merge.op != "merge" || merge.policy != "reject" || len(merge.items) != 2 || !aggregateRead(merge.items[0], remainder) {
		return false
	}
	parts := merge.items[1]
	if parts.op != "object" || len(parts.fields) != len(layout.Fields) {
		return false
	}
	for wire, core := range layout.Fields {
		field := parts.fields[wire]
		if !aggregateRead(field, aggregateMember(input, core)) || field.isRequired {
			return false
		}
	}
	return true
}

func aggregateRead(program *compiledExpression, input []string) bool {
	return program != nil && program.op == "read" && program.from == "input" && slices.Equal(program.pointer, input)
}

func aggregatePresence(program *compiledExpression, operation string, input []string) bool {
	return program != nil && program.op == operation && aggregateRead(program.source, input) && !program.source.isRequired
}

func aggregateNullTest(program *compiledExpression, input []string) bool {
	if program == nil || program.op != "equal" || len(program.items) != 2 {
		return false
	}
	left, right := program.items[0], program.items[1]
	return aggregateRead(left, input) && right.op == "literal" && right.value.IsNull()
}

func aggregateMember(owner []string, member string) []string {
	return append(append([]string(nil), owner...), member)
}

func checkPackageAggregateInput(state evaluation, output JSONOutput, effect compiledSemanticEffect) error {
	layout := effect.aggregateLayout
	if layout.nextOwner != nil {
		value, err := state.readPackageInput(layout.nextOwner)
		if err != nil {
			return err
		}
		if !value.IsZero() {
			return fmt.Errorf("aggregate owner leaves an additional array member without a representation")
		}
	}
	fields, err := coreObjectFields(output)
	if err != nil {
		return err
	}
	reader := coreOutputReader(output, state.valueReader)
	for _, label := range layout.labels {
		value, err := readOutputPointer(output, []string{label.name}, reader)
		if err != nil {
			return err
		}
		if value.IsZero() || value.node != nil || !equalValues(value.value, label.value) {
			return fmt.Errorf("aggregate label %s differs from its declared variant", label.name)
		}
	}
	for _, field := range fields {
		name := field.key
		isMapped, isDeclared := layout.members[name]
		if !isDeclared {
			return fmt.Errorf("aggregate member %s is not a declared core field", name)
		}
		if isMapped {
			continue
		}
		if field.value.IsZero() || coreOutputIsNull(field.value) {
			continue
		}
		return fmt.Errorf("aggregate member %s has no declared representation", name)
	}
	remainder, err := readOutputPointer(output, []string{layout.remainder}, reader)
	if err != nil {
		return err
	}
	if remainder.IsZero() {
		return fmt.Errorf("aggregate remainder must be present")
	}
	if coreOutputIsNull(remainder) {
		for _, field := range layout.fields {
			value, err := readOutputPointer(output, []string{field.core}, reader)
			if err != nil {
				return err
			}
			if !value.IsZero() {
				return fmt.Errorf("null aggregate remainder cannot carry projected members")
			}
		}
		return nil
	}
	if plannedJSONType(remainder) != "object" {
		return fmt.Errorf("aggregate remainder requires an object or null")
	}
	for _, field := range layout.fields {
		value, err := readOutputPointer(remainder, []string{field.wire}, reader)
		if err != nil {
			return err
		}
		if !value.IsZero() {
			return fmt.Errorf("aggregate remainder repeats projected member %s", field.wire)
		}
	}
	return nil
}
