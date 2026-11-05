package protocol

import "slices"

// Only unconditional emits may use this proof: their preflight rejects a missing
// input before another operand can supply it. A successful deep merge cannot
// overwrite the copied non-object value. The final wire schema must exclude
// objects: copying an object alone does not prove it stays whole.
func provesDeepMergeEmission(program *compiledExpression, output, input []string, schema *ValueSchema) bool {
	if program == nil || program.op != "merge_deep" {
		return false
	}
	leaf, err := schemaAtPointer(schema, output)
	if err != nil || leaf == nil || leaf.Type == AnyType || leaf.Type == ObjectType {
		return false
	}
	for _, operand := range program.items {
		if provesPackageEmission(operand, output, input) {
			return true
		}
	}
	return false
}

type mergeEmissionProof struct {
	steps    int
	input    []string
	absent   [][]string
	hasInput bool
}

// A conditional emit promises a copy only when its condition holds. Keep the
// whole top-level owner exclusive then, so even object-valued copies stay whole.
func provesGuardedDeepMergeEmission(program *compiledExpression, output, input []string, condition *compiledExpression, steps int) bool {
	if len(output) == 0 {
		return false
	}
	proof := mergeEmissionProof{steps: steps, input: input}
	if !proof.collect(condition) || !proof.hasInput {
		return false
	}
	return proof.emits(program, output)
}

func (proof *mergeEmissionProof) emits(program *compiledExpression, output []string) bool {
	if !proof.advance() || program == nil {
		return false
	}
	if provesPackageEmission(program, output, proof.input) {
		return true
	}
	if program.op == "if" {
		return proof.emits(program.then, output) && proof.emits(program.otherwise, output)
	}
	if program.op != "merge_deep" {
		return false
	}
	owner := -1
	for index, operand := range program.items {
		if !proof.advance() {
			return false
		}
		// Each operand's existing copy proof traverses its compiler-bounded AST
		// once; no aliases or conditional facts are added to that proof.
		if provesPackageEmission(operand, output, proof.input) {
			owner = index
			break
		}
	}
	if owner < 0 {
		return false
	}
	for index, operand := range program.items {
		if index != owner && !proof.omitsKey(operand, output[0]) {
			return false
		}
	}
	return true
}

func (proof *mergeEmissionProof) advance() bool {
	if proof.steps <= 0 {
		return false
	}
	proof.steps--
	return true
}

func (proof *mergeEmissionProof) collect(condition *compiledExpression) bool {
	if !proof.advance() || condition == nil {
		return false
	}
	if condition.op == "all" {
		for _, item := range condition.items {
			if !proof.collect(item) {
				return false
			}
		}
		return true
	}
	if aggregatePresence(condition, "exists", proof.input) {
		proof.hasInput = true
		return true
	}
	if condition.op != "not" || condition.source == nil || condition.source.op != "exists" {
		return false
	}
	read := condition.source.source
	if read == nil || read.op != "read" || read.from != "input" || read.isRequired {
		return false
	}
	// Do not accept a guard that directly contradicts its required source.
	if len(read.pointer) <= len(proof.input) && slices.Equal(read.pointer, proof.input[:len(read.pointer)]) {
		return false
	}
	proof.absent = append(proof.absent, read.pointer)
	return true
}

func (proof *mergeEmissionProof) omitsKey(program *compiledExpression, key string) bool {
	if !proof.advance() {
		return false
	}
	if program == nil {
		return false
	}
	switch program.op {
	case "object":
		_, hasKey := program.fields[key]
		return !hasKey
	case "project":
		return program.policy == "exclude" && slices.Contains(program.keys, key)
	case "if":
		return proof.omitsKey(program.then, key) && proof.omitsKey(program.otherwise, key)
	case "read":
		if program.from != "input" || program.isRequired {
			return false
		}
		for _, path := range proof.absent {
			if !proof.advance() {
				return false
			}
			if len(path) == len(program.pointer)+1 && path[len(program.pointer)] == key && slices.Equal(path[:len(program.pointer)], program.pointer) {
				return true
			}
		}
	}
	return false
}
