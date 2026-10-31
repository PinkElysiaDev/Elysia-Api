package protocol

import (
	"context"
	"fmt"
)

type packageTaskContracts struct {
	request, response compiledWireContract
}

type packageTaskOperationIdentity struct {
	Operation    ProtocolOperationSpec `json:"operation"`
	RequestHash  string                `json:"requestHash,omitempty"`
	ResponseHash string                `json:"responseHash"`
}

func (compiled *Compiled) compilePackageTaskMappings(spec ProtocolSpec, mapping ProtocolMapping, wireSpec compiledProtocolSpec, expressions *expressionCompiler) error {
	hasAdapters := compiled.Supports(DecodeRequest) && compiled.Supports(EncodeRequest) && compiled.Supports(DecodeResponse) && compiled.Supports(EncodeResponse)
	linked := map[string]bool{}
	for _, name := range sortedKeys(spec.Operations) {
		operation := compiled.operations[name]
		if operation.Kind != "submit" {
			continue
		}
		if err := checkTaskFlow(operation, compiled.operations, mapping.Capabilities, hasAdapters); err != nil {
			return fmt.Errorf("/spec/operations/%s/task: %w", name, err)
		}
		if compiled.taskMappings == nil {
			compiled.taskMappings = map[string]map[string]compiledMapping{}
			compiled.packagePlan.taskContracts = map[string]map[string]packageTaskContracts{}
		}
		contracts := map[string]packageTaskContracts{}
		operations := map[string]packageTaskOperationIdentity{}
		for _, reference := range []struct{ purpose, operation string }{{"submit", name}, {"status", operation.Task.Status}, {"result", operation.Task.Result}, {"cancel", operation.Task.Cancel}} {
			if reference.operation == "" {
				continue
			}
			wire := spec.Operations[reference.operation]
			if wire.Response == "" {
				return fmt.Errorf("/spec/operations/%s/response: task operations require a response document", reference.operation)
			}
			contracts[reference.purpose] = packageTaskContracts{request: wireSpec.documents[wire.Request], response: wireSpec.documents[wire.Response]}
			operations[reference.purpose] = packageTaskOperationIdentity{Operation: wire, RequestHash: wireSpec.documentHashes[wire.Request], ResponseHash: wireSpec.documentHashes[wire.Response]}
			linked[reference.operation] = true
		}
		compiled.packagePlan.taskContracts[name] = contracts
		compiled.taskMappings[name] = map[string]compiledMapping{}
		programs := mapping.Operations[name].Task
		for _, item := range []struct {
			kind    string
			program Expression
		}{{"decode", programs.Decode}, {"encode", programs.Encode}, {"control", programs.Control}} {
			path := "/mapping/operations/" + name + "/task/" + item.kind
			references := map[string]Expression{}
			expressions.mappingReferences = references
			plan, err := expressions.compilePlan(item.program, path, expressionScope{}, 1)
			expressions.mappingReferences = nil
			if err != nil {
				return err
			}
			identity, err := EncodeValue(struct {
				CompilerVersion string                                  `json:"compilerVersion"`
				CoreVersion     int                                     `json:"coreVersion"`
				DSLVersion      int                                     `json:"dslVersion"`
				Kind            string                                  `json:"kind"`
				Operations      map[string]packageTaskOperationIdentity `json:"operations"`
				Program         Expression                              `json:"program"`
				References      map[string]Expression                   `json:"references"`
			}{CompilerVersion, mapping.CoreVersion, mapping.DSLVersion, item.kind, operations, item.program, references})
			if err != nil {
				return err
			}
			compiled.taskMappings[name][item.kind] = compiledMapping{outputPlan: plan, definitionHash: hashValue(identity)}
		}
	}
	for name, operation := range compiled.operations {
		if (operation.Kind == "status" || operation.Kind == "result" || operation.Kind == "cancel") && !linked[name] {
			return fmt.Errorf("/spec/operations/%s: task control operation has no declared submit flow", name)
		}
	}
	return compiled.checkTaskSamples(mapping.TaskSamples)
}

func (compiled *Compiled) executePackageTask(ctx context.Context, operation, kind, purpose string, mapping compiledMapping, input Value) (Value, error) {
	contracts, exists := compiled.packagePlan.taskContracts[operation][purpose]
	if !exists {
		return Value{}, fmt.Errorf("task purpose has no declared operation")
	}
	path := "/mapping/operations/" + operation + "/task/" + kind
	direction := EncodeResponse
	var source, target compiledWireContract
	if kind == "decode" {
		direction, source = DecodeResponse, contracts.response
	} else if kind == "encode" {
		target = contracts.response
	}
	contextValue, err := EncodeValue(Object{"operation": StringValue(purpose)})
	if err != nil {
		return Value{}, err
	}
	output, err := compiled.executePackageOperationPlan(ctx, mapping, direction, path, input, contextValue, source, target)
	if err != nil || kind != "control" || contracts.request.contextValidator == nil {
		return output, err
	}
	// The control program returns a transport envelope. Only its body is a wire
	// document; query/auth merging stays in the existing task transport.
	body, err := readValuePointerWithObjectReader(output, []string{"body"}, requestValueReader(output), nil)
	if err != nil {
		return Value{}, err
	}
	if err := contracts.request.contextValidator.validate(ctx, EncodeRequest, body, compiled.limits); err != nil {
		return Value{}, compiled.runtimeError(EncodeRequest, err)
	}
	return output, nil
}
