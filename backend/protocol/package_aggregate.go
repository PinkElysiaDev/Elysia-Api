package protocol

import (
	"fmt"
	"slices"
	"strconv"
)

// The first aggregate contract is a single request cache owner. Its variant
// labels belong to the core vocabulary; wire member names remain authored data.
func validatePackageAggregateLayout(direction Direction, core []string, layout *ProtocolAggregateLayout) error {
	if layout == nil {
		return fmt.Errorf("aggregate requires an explicit layout")
	}
	if (direction != DecodeRequest && direction != EncodeRequest) || !slices.Equal(core, []string{"cache", "0"}) ||
		layout.Remainder != "value" || len(layout.Fields) != 1 || len(layout.Labels) != 2 ||
		!layout.Labels["kind"].IsString("breakpoint") || !layout.Labels["location"].IsString("request") {
		return fmt.Errorf("unsupported core aggregate layout")
	}
	for _, member := range layout.Fields {
		if member != "ttl" {
			return fmt.Errorf("unsupported core aggregate field")
		}
	}
	return nil
}

func provesPackageAggregate(program *compiledExpression, direction Direction, effect compiledSemanticEffect) bool {
	input, output := effect.corePointer, effect.wirePointer
	if direction == DecodeRequest {
		input, output = output, input
	}
	if effect.when == nil || !aggregatePresence(effect.when.expression, "exists", input) {
		return false
	}
	selected, hasGuard := aggregateOutput(program, output, input)
	if selected == nil || !hasGuard {
		return false
	}
	if direction == DecodeRequest {
		return provesObjectPartition(selected, input, effect.layout)
	}
	return provesObjectReconstruction(selected, input, effect.layout)
}

// Only the matching optional-owner guard is discharged. Other conditionals or
// possibly omitted preceding array entries cannot establish this output path.
func aggregateOutput(program *compiledExpression, output, input []string) (*compiledExpression, bool) {
	if program == nil {
		return nil, false
	}
	if program.op == "if" && program.otherwise == nil && aggregatePresence(program.when, "exists", input) {
		selected, _ := aggregateOutput(program.then, output, input)
		return selected, true
	}
	if len(output) == 0 {
		return program, false
	}
	switch program.op {
	case "object":
		return aggregateOutput(program.fields[output[0]], output[1:], input)
	case "array":
		index, err := strconv.Atoi(output[0])
		if err != nil || index != 0 || len(program.items) != 1 || strconv.Itoa(index) != output[0] {
			return nil, false
		}
		return aggregateOutput(program.items[index], output[1:], input)
	}
	return nil, false
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

func checkPackageAggregateInput(state evaluation, output JSONOutput) error {
	second, err := state.readPackageInput([]string{"cache", "1"})
	if err != nil {
		return err
	}
	if !second.IsZero() {
		return fmt.Errorf("one wire cache policy cannot express multiple intents")
	}
	var intent CacheIntent
	hasValidatedCore := state.inputReader != nil
	if hasValidatedCore {
		// Both EncodeRequestValue and the native replay's finishDecodedRequest
		// validate hints before entering this read-only typed plan.
		kind, readErr := readOutputPointer(output, []string{"kind"}, nil)
		if readErr != nil {
			return readErr
		}
		err = kind.value.Decode(&intent.Kind)
	} else {
		intent, err = readAggregateCacheIntent(output)
	}
	if err != nil {
		return err
	}
	if intent.Kind != "breakpoint" {
		return fmt.Errorf("cache aggregate requires a breakpoint intent")
	}
	if hasValidatedCore {
		return nil
	}
	// Location is descriptive metadata. The existing encoder does not reject a
	// changed location, so reconstruction must not add a stricter label check.
	return validateRequestHints(&Request{Cache: []CacheIntent{intent}})
}

func readAggregateCacheIntent(output JSONOutput) (CacheIntent, error) {
	var intent CacheIntent
	fields, err := coreObjectFields(output)
	if err != nil {
		return intent, err
	}
	reader := coreOutputReader(output, nil)
	for _, field := range fields {
		// Mapping pointers are exact, unlike the contract decoder's folded Go
		// field aliases. Raw execution must not validate TTL then read /ttl as
		// absent and discard it; typed input already has these canonical names.
		if !slices.Contains([]string{"kind", "location", "value", "ttl", "resource"}, field.key) {
			return intent, fmt.Errorf("cache aggregate requires canonical core members")
		}
		if err := writeCoreCacheIntentField(field, &intent, reader); err != nil {
			return intent, err
		}
	}
	return intent, nil
}
