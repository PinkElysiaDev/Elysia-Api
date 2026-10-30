package protocol

import (
	"context"
	"fmt"
)

func (compiled *Compiled) compilePackageModelMappings(spec ProtocolSpec, mapping ProtocolMapping, documents map[string]compiledWireContract, expressions *expressionCompiler) error {
	for _, name := range sortedKeys(spec.Operations) {
		operation := spec.Operations[name]
		if operation.Kind != "models" {
			continue
		}
		path := "/mapping/operations/" + escapePointer(name) + "/decodeModels"
		wire := spec.Documents[operation.Response]
		program := *mapping.Operations[name].DecodeModels
		references := map[string]Expression{}
		expressions.mappingReferences = references
		plan, err := expressions.compilePlan(program, path, expressionScope{input: wire.Schema, root: wire.Schema}, 1)
		expressions.mappingReferences = nil
		if err != nil {
			return err
		}
		if err := checkExpressionOutput(plan.expression, &modelPageSchema, compiled.limits); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		identity, err := EncodeValue(struct {
			CompilerVersion string                `json:"compilerVersion"`
			Document        string                `json:"document"`
			Wire            WireContractSpec      `json:"wire"`
			Program         Expression            `json:"program"`
			References      map[string]Expression `json:"references"`
		}{CompilerVersion, operation.Response, wire, program, references})
		if err != nil {
			return err
		}
		if compiled.modelMappings == nil {
			compiled.modelMappings = map[string]compiledMapping{}
			compiled.packagePlan.modelContracts = map[string]compiledWireContract{}
		}
		compiled.modelMappings[name] = compiledMapping{outputPlan: plan, definitionHash: hashValue(identity)}
		compiled.packagePlan.modelContracts[name] = documents[operation.Response]
	}
	if err := compiled.prepareModelEvidence(mapping.ModelSamples); err != nil {
		return fmt.Errorf("/mapping/modelSamples: %w", err)
	}
	return nil
}

// A model page has its own output contract, not Request/Response schemaVersion
// or native replay. It still uses the shared wire validator and expression plan.
func (compiled *Compiled) executePackageModelMapping(ctx context.Context, operation string, mapping compiledMapping, input Value) (Value, error) {
	path := "/mapping/operations/" + escapePointer(operation) + "/decodeModels"
	fail := func(err error) (Value, error) {
		return Value{}, compiled.mappingError(DecodeResponse, locateMappingError(path, err))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := checkResourceValue(input, compiled.limits, "mapping_input"); err != nil {
		return fail(err)
	}
	contract := compiled.packagePlan.modelContracts[operation]
	if err := contract.contextValidator.validate(ctx, DecodeResponse, input, compiled.limits); err != nil {
		return Value{}, compiled.runtimeError(DecodeResponse, err)
	}
	state := evaluation{ctx: ctx, input: input, root: input, context: Value{raw: "null"},
		valueReader: requestValueReader(input), objectReader: mapping.outputPlan.newRootObjectReader(input),
		budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	output, err := mapping.outputPlan.evaluate(state)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkResourceValue(output, compiled.limits, "mapping_output"); err != nil {
		return fail(err)
	}
	return output, nil
}
