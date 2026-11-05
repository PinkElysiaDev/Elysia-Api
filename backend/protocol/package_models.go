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
		binding := mapping.Operations[name]
		entry, err := compileOperationProgram(name, "decodeModels", operation.Response, *binding.DecodeModels,
			wireSpec.resolvedDocuments[operation.Response].Schema, &modelPageSchema, wireSpec, expressions)
		if err != nil {
			return err
		}
		if compiled.modelMappings == nil {
			compiled.modelMappings = map[string]compiledMapping{}
			compiled.packagePlan.modelContracts = map[string]compiledWireContract{}
		}
		compiled.modelMappings[name] = entry
		compiled.packagePlan.modelContracts[name] = wireSpec.documents[operation.Response]
		if binding.EncodeModels != nil {
			document := operation.Models.ClientDocument
			entry, err := compileOperationProgram(name, "encodeModels", document, *binding.EncodeModels,
				&modelPageSchema, wireSpec.resolvedDocuments[document].Schema, wireSpec, expressions)
			if err != nil {
				return err
			}
			if compiled.modelEncoders == nil {
				compiled.modelEncoders = map[string]compiledMapping{}
				compiled.packagePlan.modelClientContracts = map[string]compiledWireContract{}
			}
			compiled.modelEncoders[name] = entry
			compiled.packagePlan.modelClientContracts[name] = wireSpec.documents[document]
		}
	}
	if err := compiled.prepareModelEvidence(mapping.ModelSamples); err != nil {
		return fmt.Errorf("/mapping/modelSamples: %w", err)
	}
	return nil
}

func compileOperationProgram(operation, field, document string, program Expression, input, output *ValueSchema, wireSpec compiledProtocolSpec, expressions *expressionCompiler) (compiledMapping, error) {
	path := "/mapping/operations/" + escapePointer(operation) + "/" + field
	references := map[string]Expression{}
	expressions.mappingReferences = references
	plan, err := expressions.compilePlan(program, path, expressionScope{input: input, root: input}, 1)
	expressions.mappingReferences = nil
	if err != nil {
		return compiledMapping{}, err
	}
	if err := checkExpressionOutput(plan.expression, output, expressions.limits); err != nil {
		return compiledMapping{}, fmt.Errorf("%s: %w", path, err)
	}
	identity, err := EncodeValue(struct {
		CompilerVersion string                `json:"compilerVersion"`
		Field           string                `json:"field"`
		Document        string                `json:"document"`
		WireHash        string                `json:"wireHash"`
		Program         Expression            `json:"program"`
		References      map[string]Expression `json:"references"`
	}{CompilerVersion, field, document, wireSpec.documentHashes[document], program, references})
	if err != nil {
		return compiledMapping{}, err
	}
	return compiledMapping{outputPlan: plan, definitionHash: hashValue(identity)}, nil
}

// CanEncodeModelPage reports whether the operation declares a client catalog.
// Upstream discovery alone does not expose a local catalog endpoint.
func (compiled *Compiled) CanEncodeModelPage(operation string) bool {
	_, exists := compiled.modelEncoders[operation]
	return exists
}

// EncodeModelPage renders a host catalog with the pinned operation's Mapping
// and validates its final client document. It never calls a provider or codec.
func (compiled *Compiled) EncodeModelPage(ctx context.Context, operation string, page ModelPage) (Value, error) {
	mapping, exists := compiled.modelEncoders[operation]
	if !exists {
		return Value{}, operationMetadataError("/operations/"+escapePointer(operation), "operation has no declared client catalog encoder")
	}
	if err := compiled.validateModelPage(operation, page); err != nil {
		return Value{}, compiled.mappingError(EncodeResponse, err)
	}
	input, err := EncodeValue(page)
	if err != nil {
		return Value{}, err
	}
	path := "/mapping/operations/" + escapePointer(operation) + "/encodeModels"
	return compiled.executePackageOperationPlan(ctx, mapping, EncodeResponse, path, input, Value{raw: "null"}, compiledWireContract{}, compiled.packagePlan.modelClientContracts[operation], EvaluationContext{})
}

// A model page has its own output contract, not Request/Response schemaVersion
// or native replay. It still uses the shared wire validator and expression plan.
func (compiled *Compiled) executePackageModelMapping(ctx context.Context, operation string, mapping compiledMapping, input Value) (Value, error) {
	path := "/mapping/operations/" + escapePointer(operation) + "/decodeModels"
	return compiled.executePackageOperationPlan(ctx, mapping, DecodeResponse, path, input, Value{raw: "null"}, compiled.packagePlan.modelContracts[operation], compiledWireContract{}, EvaluationContext{})
}
