package protocol

import "fmt"

type compiledDeliveryRange struct {
	measure, texts, ranges *compiledOutputPlan
	measuresUTF16          bool
}

func compileDeliveryRanges(declarations []ProtocolDeliveryRange, direction Direction, wire *ValueSchema, path string, compiler *expressionCompiler) ([]compiledDeliveryRange, error) {
	if len(declarations) == 0 {
		return nil, nil
	}
	if direction != EncodeResponse && direction != EncodeEvent {
		return nil, fmt.Errorf("%s: requires response or event encoding", path)
	}
	seen := make(map[string]bool, len(declarations))
	compiled := make([]compiledDeliveryRange, 0, len(declarations))
	textSchema := deliveryRecordSchema(map[string]JSONType{"key": StringType, "text": StringType, "mode": StringType, "path": StringType})
	rangeSchema := deliveryRecordSchema(map[string]JSONType{"key": StringType, "id": StringType, "start": NumberType, "end": NumberType, "path": StringType})
	for index, declaration := range declarations {
		at := fmt.Sprintf("%s/%d", path, index)
		if declaration.Name == "" || seen[declaration.Name] {
			return nil, fmt.Errorf("%s/name: requires a unique nonempty channel", at)
		}
		seen[declaration.Name] = true
		entry := compiledDeliveryRange{}
		var err error
		stringSchema := &ValueSchema{Type: StringType}
		entry.measure, err = compiler.compilePlan(declaration.Measure, at+"/measure", expressionScope{input: stringSchema, root: stringSchema, isWireContract: true}, 1)
		if err != nil {
			return nil, err
		}
		if !isAssignable(entry.measure.expression.result, NumberType) {
			return nil, fmt.Errorf("%s/measure: requires an integer length", at)
		}
		entry.measuresUTF16 = entry.measure.expression.op == "utf16_length" && entry.measure.source.isInputIdentity()
		for _, selector := range []struct {
			name   string
			value  Expression
			schema *ValueSchema
			output **compiledOutputPlan
		}{{"texts", declaration.Texts, textSchema, &entry.texts}, {"ranges", declaration.Ranges, rangeSchema, &entry.ranges}} {
			plan, err := compiler.compilePlan(selector.value, at+"/"+selector.name, expressionScope{input: wire, root: wire, isWireContract: true}, 1)
			if err != nil {
				return nil, err
			}
			if err := checkExpressionOutput(plan.expression, selector.schema, compiler.limits); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", at, selector.name, err)
			}
			*selector.output = plan
		}
		compiled = append(compiled, entry)
	}
	return compiled, nil
}

func deliveryRecordSchema(fields map[string]JSONType) *ValueSchema {
	record := &ValueSchema{Type: ObjectType, Properties: make(map[string]ValueSchema, len(fields)), Required: sortedKeys(fields)}
	for name, kind := range fields {
		record.Properties[name] = ValueSchema{Type: kind}
	}
	return &ValueSchema{Type: ArrayType, Items: record}
}
