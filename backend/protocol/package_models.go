package protocol

import (
	"context"
	"fmt"
)

func (compiled *Compiled) compilePackageModelMappings(spec ProtocolSpec, mapping ProtocolMapping, wireSpec compiledProtocolSpec, expressions *expressionCompiler) error {
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
			WireHash        string                `json:"wireHash"`
			Program         Expression            `json:"program"`
			References      map[string]Expression `json:"references"`
		}{CompilerVersion, operation.Response, wireSpec.documentHashes[operation.Response], program, references})
		if err != nil {
			return err
		}
		if compiled.modelMappings == nil {
			compiled.modelMappings = map[string]compiledMapping{}
			compiled.packagePlan.modelContracts = map[string]compiledWireContract{}
		}
		compiled.modelMappings[name] = compiledMapping{outputPlan: plan, definitionHash: hashValue(identity)}
		compiled.packagePlan.modelContracts[name] = wireSpec.documents[operation.Response]
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
	return compiled.executePackageOperationPlan(ctx, mapping, DecodeResponse, path, input, Value{raw: "null"}, compiled.packagePlan.modelContracts[operation], compiledWireContract{})
}
