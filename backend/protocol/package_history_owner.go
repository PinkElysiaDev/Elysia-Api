package protocol

import "slices"

type historyOwnerGoal struct {
	isAbsent bool
	value    Value
	input    []string
}

type historyOwnerBinding struct {
	slot        int
	source      *compiledExpression
	sourceScope *historyOwnerBinding
	parent      *historyOwnerBinding
}

type historyOwnerProof struct {
	direction Direction
	effect    compiledSemanticEffect
	steps     int
}

// Local owners need only fixed paths and the declared whole-value partition.
// Bound alias expansion by the existing compiler budget, without runtime work.
func provesLocalHistoryOwner(program *compiledExpression, direction Direction, effect compiledSemanticEffect, steps int) bool {
	proof := historyOwnerProof{direction: direction, effect: effect, steps: steps}
	unmatched, _ := parsePointer(effect.valueMapping.Unmatched)
	absent := historyOwnerGoal{isAbsent: true}
	if direction == DecodeRequest {
		return aggregatePresence(effect.when.expression, "exists", effect.wirePointer) &&
			proof.field(program, nil, effect.corePointer, historyOwnerGoal{value: effect.valueMapping.Value}, true, true) &&
			proof.field(program, nil, effect.corePointer, absent, true, false) &&
			proof.field(program, nil, unmatched, absent, true, true) &&
			proof.field(program, nil, unmatched, historyOwnerGoal{input: effect.wirePointer}, true, false)
	}
	if !aggregatePresence(effect.when.expression, "exists", effect.corePointer) ||
		!proof.field(program, nil, effect.wirePointer, historyOwnerGoal{input: unmatched}, true, false) {
		return false
	}
	for _, variant := range effect.valueMapping.Variants {
		if proof.field(program, nil, effect.wirePointer, historyOwnerGoal{value: variant}, true, true) {
			return true
		}
	}
	return false
}

func (proof *historyOwnerProof) field(program *compiledExpression, scope *historyOwnerBinding, output []string, goal historyOwnerGoal, hasCase, isRecognized bool) bool {
	if proof.steps <= 0 {
		return false
	}
	proof.steps--
	if program == nil || program.op == "omit" {
		return goal.isAbsent
	}
	switch program.op {
	case "let":
		binding := historyOwnerBinding{slot: program.bindingSlot, source: program.source, sourceScope: scope, parent: scope}
		return proof.field(program.body, &binding, output, goal, hasCase, isRecognized)
	case "read":
		if program.from == "binding" {
			for binding := scope; binding != nil; binding = binding.parent {
				if binding.slot == program.bindingSlot {
					path := append(slices.Clone(program.pointer), output...)
					return proof.field(binding.source, binding.sourceScope, path, goal, hasCase, isRecognized)
				}
			}
			return false
		}
		return goal.input != nil && provesPackageEmission(program, output, goal.input)
	case "if":
		if hasCase && proof.isPartition(program.when) {
			branch := program.otherwise
			if isRecognized {
				branch = program.then
			}
			return proof.field(branch, scope, output, goal, hasCase, isRecognized)
		}
		if len(output) > 0 && omitsEmptyHistoryParent(program) {
			return proof.field(program.otherwise, scope, output, goal, hasCase, isRecognized)
		}
		return proof.field(program.then, scope, output, goal, hasCase, isRecognized) &&
			proof.field(program.otherwise, scope, output, goal, hasCase, isRecognized)
	}
	if len(output) == 0 {
		return !goal.value.IsZero() && program.op == "literal" && equalValues(program.value, goal.value)
	}
	switch program.op {
	case "literal":
		return goal.isAbsent && isEmptyHistoryObject(program)
	case "object":
		return proof.field(program.fields[output[0]], scope, output[1:], goal, hasCase, isRecognized)
	case "project":
		isListed := slices.Contains(program.keys, output[0])
		if (program.policy == "include" && !isListed) || (program.policy == "exclude" && isListed) {
			return goal.isAbsent
		}
		return proof.field(program.source, scope, output, goal, hasCase, isRecognized)
	case "merge":
		return proof.mergedField(program, scope, output, goal, hasCase, isRecognized)
	}
	return false
}

func (proof *historyOwnerProof) isPartition(condition *compiledExpression) bool {
	if proof.direction == DecodeRequest {
		return provesWholeValueVariants(condition, proof.effect.wirePointer, proof.effect.valueMapping.Variants)
	}
	return aggregatePresence(condition, "exists", proof.effect.corePointer)
}

func (proof *historyOwnerProof) mergedField(program *compiledExpression, scope *historyOwnerBinding, output []string, goal historyOwnerGoal, hasCase, isRecognized bool) bool {
	if program.policy != "reject" {
		return false
	}
	var owner *compiledExpression
	for _, item := range program.items {
		// Merge is shallow: a different child of the same parent still collides.
		if proof.field(item, scope, output[:1], historyOwnerGoal{isAbsent: true}, false, false) {
			continue
		}
		if owner != nil {
			return false
		}
		owner = item
	}
	return proof.field(owner, scope, output, goal, hasCase, isRecognized)
}

func omitsEmptyHistoryParent(program *compiledExpression) bool {
	if program.when == nil || program.when.op != "equal" || len(program.when.items) != 2 ||
		program.then == nil || program.then.op != "omit" || program.otherwise == nil {
		return false
	}
	left, right := program.when.items[0], program.when.items[1]
	if isEmptyHistoryObject(left) {
		left, right = right, left
	}
	otherwise := program.otherwise
	return isEmptyHistoryObject(right) && left.op == "read" && left.from == "binding" && !left.isRequired &&
		otherwise.op == "read" && otherwise.from == "binding" && !otherwise.isRequired &&
		left.bindingSlot == otherwise.bindingSlot && slices.Equal(left.pointer, otherwise.pointer)
}

func isEmptyHistoryObject(program *compiledExpression) bool {
	return program != nil && ((program.op == "object" && len(program.fields) == 0) ||
		(program.op == "literal" && equalValues(program.value, Value{raw: "{}"})))
}
