package protocol

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
