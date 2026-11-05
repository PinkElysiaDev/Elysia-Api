package protocol

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// ProtocolValueMapping declares a finite whole-value representation of one
// core value. Unmatched names the core parameter that retains other wire values.
type ProtocolValueMapping struct {
	Value     Value   `json:"value"`
	Variants  []Value `json:"variants"`
	Unmatched string  `json:"unmatched"`
}

func validateFiniteValueMapping(direction Direction, path string, mapping *ProtocolValueMapping, limits Limits) error {
	if mapping == nil || (direction != DecodeRequest && direction != EncodeRequest) || mapping.Value.IsZero() {
		return fmt.Errorf("finite value mappings require a request field and a present core value")
	}
	pointer, err := parsePointer(path)
	if err != nil {
		return err
	}
	kind := mappedRequestFieldType(pointer)
	if kind == nil {
		return fmt.Errorf("finite value mapping requires an explicit semantic field")
	}
	if kind != reflect.TypeFor[Value]() {
		if mapping.Value.IsNull() {
			return fmt.Errorf("finite value mapping cannot assign null to a scalar core field")
		}
		if err := mapping.Value.Decode(reflect.New(kind).Interface()); err != nil {
			return fmt.Errorf("finite value mapping conflicts with its core field type: %w", err)
		}
	}
	if len(mapping.Variants) == 0 || len(mapping.Variants) > limits.Mutations || !strings.HasPrefix(mapping.Unmatched, "/parameters/") {
		return fmt.Errorf("value mapping requires bounded wire variants and an unmatched parameter path")
	}
	unmatched, err := parsePointer(mapping.Unmatched)
	if err != nil {
		return err
	}
	if slices.Equal(pointer, unmatched) {
		return fmt.Errorf("canonical field and unmatched wire value require separate owners")
	}
	for index, value := range mapping.Variants {
		if value.IsZero() {
			return fmt.Errorf("value mapping cannot recognize an absent wire value")
		}
		for _, earlier := range mapping.Variants[:index] {
			if equalValues(earlier, value) {
				return fmt.Errorf("value mapping has duplicate wire variants")
			}
		}
	}
	return nil
}

func mappedRequestFieldType(pointer []string) reflect.Type {
	if len(pointer) == 0 || slices.Contains([]string{"schemaVersion", "source", "native", "resources", "clientOutput"}, pointer[0]) {
		return nil
	}
	kind := reflect.TypeFor[Request]()
	for _, key := range pointer {
		for kind.Kind() == reflect.Pointer {
			kind = kind.Elem()
		}
		if kind == reflect.TypeFor[Object]() {
			kind = reflect.TypeFor[Value]()
			continue
		}
		if kind.Kind() != reflect.Struct || kind == reflect.TypeFor[Value]() {
			return nil
		}
		var fieldType reflect.Type
		for index := range kind.NumField() {
			field := kind.Field(index)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.IsExported() && name == key {
				fieldType = field.Type
				break
			}
		}
		if fieldType == nil {
			return nil
		}
		kind = fieldType
	}
	if kind == reflect.TypeFor[Value]() || slices.Contains([]reflect.Kind{reflect.String, reflect.Bool, reflect.Int, reflect.Int64}, kind.Kind()) {
		return kind
	}
	return nil
}

func provesHistoryValueMapping(program *compiledExpression, direction Direction, effect compiledSemanticEffect, steps int) bool {
	mapping := effect.valueMapping
	if program == nil || mapping == nil || effect.when == nil {
		return false
	}
	if program.op != "if" || (direction == EncodeRequest && !aggregatePresence(program.when, "exists", effect.corePointer)) {
		return provesLocalHistoryOwner(program, direction, effect, steps)
	}
	unknown, _ := parsePointer(mapping.Unmatched)
	if direction == DecodeRequest {
		return aggregatePresence(effect.when.expression, "exists", effect.wirePointer) &&
			provesWholeValueVariants(program.when, effect.wirePointer, mapping.Variants) &&
			provesLiteralField(program.then, effect.corePointer, mapping.Value) &&
			provesAbsentField(program.otherwise, effect.corePointer) &&
			provesAbsentField(program.then, unknown) &&
			provesCopiedField(program.otherwise, unknown, effect.wirePointer)
	}
	if !aggregatePresence(effect.when.expression, "exists", effect.corePointer) || !provesCopiedField(program.otherwise, effect.wirePointer, unknown) {
		return false
	}
	for _, variant := range mapping.Variants {
		if provesLiteralField(program.then, effect.wirePointer, variant) {
			return true
		}
	}
	return false
}

func provesWholeValueVariants(condition *compiledExpression, input []string, variants []Value) bool {
	if condition == nil {
		return false
	}
	comparisons := condition.items
	if condition.op == "equal" {
		comparisons = []*compiledExpression{condition}
	} else if condition.op != "any" {
		return false
	}
	if len(comparisons) != len(variants) {
		return false
	}
	matched := make([]bool, len(variants))
	for _, comparison := range comparisons {
		if comparison.op != "equal" || len(comparison.items) != 2 {
			return false
		}
		left, right := comparison.items[0], comparison.items[1]
		if left.op == "literal" {
			left, right = right, left
		}
		if !aggregateRead(left, input) || left.isRequired || right.op != "literal" {
			return false
		}
		found := false
		for index, variant := range variants {
			if !matched[index] && equalValues(right.value, variant) {
				matched[index], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func provesLiteralField(program *compiledExpression, output []string, value Value) bool {
	if program == nil {
		return false
	}
	if len(output) == 0 {
		return program.op == "literal" && equalValues(program.value, value)
	}
	if program.op == "merge" && program.policy == "last" && len(program.items) > 0 {
		return provesLiteralField(program.items[len(program.items)-1], output, value)
	}
	return program.op == "object" && provesLiteralField(program.fields[output[0]], output[1:], value)
}

func provesAbsentField(program *compiledExpression, output []string) bool {
	if program == nil || program.op == "omit" {
		return true
	}
	if len(output) == 0 {
		return false
	}
	if program.op == "object" {
		return provesAbsentField(program.fields[output[0]], output[1:])
	}
	if program.op == "if" {
		return provesAbsentField(program.then, output) && provesAbsentField(program.otherwise, output)
	}
	if program.op == "merge" {
		for _, item := range program.items {
			if !provesAbsentField(item, output) {
				return false
			}
		}
		return true
	}
	return program.op == "project" && ((program.policy == "exclude" && slices.Contains(program.keys, output[0])) || (program.policy == "include" && !slices.Contains(program.keys, output[0])))
}

func provesCopiedField(program *compiledExpression, output, input []string) bool {
	if program == nil {
		return false
	}
	if program.op == "object" && len(output) > 0 {
		return provesCopiedField(program.fields[output[0]], output[1:], input)
	}
	if program.op == "merge" && program.policy == "last" && len(program.items) > 0 {
		for _, item := range program.items[:len(program.items)-1] {
			if !provesAbsentField(item, output) {
				return false
			}
		}
		return provesCopiedField(program.items[len(program.items)-1], output, input)
	}
	if program.op == "project" && len(output) > 0 {
		isIncluded := slices.Contains(program.keys, output[0])
		if (program.policy == "include" && !isIncluded) || (program.policy == "exclude" && isIncluded) {
			return false
		}
		return provesPackageEmission(program.source, output, input)
	}
	return provesPackageEmission(program, output, input)
}

func checkHistoryValueInput(state evaluation, effect compiledSemanticEffect, output JSONOutput) error {
	if output.node != nil || !equalValues(output.value, effect.valueMapping.Value) {
		return fmt.Errorf("core field differs from the declared representation")
	}
	unmatched, err := state.readPackageInput(effect.valueUnmatched)
	if err != nil {
		return err
	}
	if !unmatched.IsZero() {
		return fmt.Errorf("canonical core field conflicts with an unmatched wire value")
	}
	return nil
}
