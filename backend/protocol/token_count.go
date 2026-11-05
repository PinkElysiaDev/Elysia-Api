package protocol

import (
	"context"
	"fmt"
)

// TokenCount is the provider-reported size of the counted input, not generated
// usage. A successful count must never be charged as a model generation.
type TokenCount struct {
	InputTokens int64 `json:"inputTokens"`
}

// ProtocolTokenCountMapping gives counting its own request contracts and result
// programs. Generation-only requirements cannot leak into a count operation.
type ProtocolTokenCountMapping struct {
	Request            map[Direction]ProtocolDirectionMapping `json:"request"`
	DecodeResponse     Expression                             `json:"decodeResponse"`
	EncodeResponse     Expression                             `json:"encodeResponse"`
	ConversionDefaults []ProtocolConversionDefault            `json:"conversionDefaults,omitempty"`
}

type compiledTokenCounting struct {
	request          *Compiled
	decode, encode   compiledMapping
	responseContract compiledWireContract
	resultValidator  *runtimeValueSchema
}

var tokenCountSchema = ValueSchema{Type: ObjectType, Required: []string{"inputTokens"}, Properties: map[string]ValueSchema{
	"inputTokens": {Type: IntegerType},
}}

func (compiled *Compiled) compileTokenCounting(document ProtocolPackage, wireSpec compiledProtocolSpec, expressions *expressionCompiler) error {
	for _, name := range sortedKeys(document.Spec.Operations) {
		operation := document.Spec.Operations[name]
		if operation.Kind != "count_tokens" {
			continue
		}
		mapping := document.Mapping.Operations[name].CountTokens
		request, err := compiled.compileTokenCountRequest(document, name, wireSpec, expressions)
		if err != nil {
			return err
		}
		count := &compiledTokenCounting{request: request, responseContract: wireSpec.documents[operation.Response]}
		count.decode, err = compileOperationProgram(name, "countTokens/decodeResponse", operation.Response, mapping.DecodeResponse,
			wireSpec.resolvedDocuments[operation.Response].Schema, &tokenCountSchema, wireSpec, expressions)
		if err != nil {
			return err
		}
		count.encode, err = compileOperationProgram(name, "countTokens/encodeResponse", operation.Response, mapping.EncodeResponse,
			&tokenCountSchema, wireSpec.resolvedDocuments[operation.Response].Schema, wireSpec, expressions)
		if err != nil {
			return err
		}
		count.resultValidator, err = compileValueSchema(&tokenCountSchema, compiled.limits)
		if err != nil {
			return err
		}
		if compiled.packagePlan.tokenCounts == nil {
			compiled.packagePlan.tokenCounts = make(map[string]*compiledTokenCounting)
		}
		compiled.packagePlan.tokenCounts[name] = count
	}
	seen := make(map[string]bool)
	for _, sample := range document.Mapping.TokenCountSamples {
		if sample.ID == "" || seen[sample.ID] || compiled.packagePlan.tokenCounts[sample.Operation] == nil ||
			sample.Input.IsZero() || sample.Expected.IsZero() == (sample.ExpectedIssue == "") || sample.Sequence || len(sample.Capabilities) != 0 ||
			(sample.Direction != DecodeRequest && sample.Direction != EncodeRequest && sample.Direction != DecodeResponse && sample.Direction != EncodeResponse) {
			return fmt.Errorf("/mapping/tokenCountSamples: invalid sample identity, operation, direction or assertion")
		}
		seen[sample.ID] = true
	}
	return nil
}

func (compiled *Compiled) compileTokenCountRequest(document ProtocolPackage, name string, wireSpec compiledProtocolSpec, expressions *expressionCompiler) (*Compiled, error) {
	operation := document.Spec.Operations[name]
	mapping := document.Mapping.Operations[name].CountTokens
	path := "/mapping/operations/" + escapePointer(name) + "/countTokens"
	if len(mapping.Request) != 2 {
		return nil, fmt.Errorf("%s/request: requires decode_request and encode_request", path)
	}
	request := &Compiled{
		identity: compiled.identity, limits: compiled.limits, explicitLimits: compiled.explicitLimits,
		capabilities: compiled.capabilities, native: compiled.native,
		operations: map[string]Operation{name: compiled.operations[name]},
		mappings:   make(map[Direction]compiledMapping, 2), wireContracts: make(map[Direction]compiledWireContract, 2),
		packagePlan: &compiledPackagePlan{coreVersion: ProtocolCoreVersion, dslVersion: ProtocolDSLVersion, effects: make(map[Direction][]compiledSemanticEffect, 2)},
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		declaration, exists := mapping.Request[direction]
		if !exists || declaration.Document != operation.Request {
			return nil, fmt.Errorf("%s/request/%s: requires the operation request document", path, direction)
		}
		wire := wireSpec.resolvedDocuments[operation.Request]
		finalWire := wire
		if declaration.FinalDocument != "" {
			var exists bool
			finalWire, exists = wireSpec.resolvedDocuments[declaration.FinalDocument]
			if !exists {
				return nil, fmt.Errorf("%s/request/%s/finalDocument: missing document", path, direction)
			}
		}
		entry, effects, err := compileProtocolProgram(path+"/request/"+string(direction), declaration, direction, wire, finalWire, wireSpec.documentHashes, nil, expressions, ProtocolCoreVersion, ProtocolDSLVersion)
		if err != nil {
			return nil, err
		}
		entry.capabilities = compiled.capabilities
		if declaration.Capabilities != nil {
			for capability := range declaration.Capabilities {
				if !compiled.capabilities[capability] {
					return nil, fmt.Errorf("%s/request/%s/capabilities: undeclared capability %q", path, direction, capability)
				}
			}
			entry.capabilities = declaration.Capabilities
		}
		request.mappings[direction], request.packagePlan.effects[direction] = entry, effects
		request.wireContracts[direction] = wireSpec.documents[operation.Request]
		if err := request.bindFinalDocument(direction, declaration, wireSpec.documents); err != nil {
			return nil, err
		}
	}
	countDocument := document
	countDocument.Mapping.Directions = mapping.Request
	countDocument.Mapping.ConversionDefaults = mapping.ConversionDefaults
	countDocument.Mapping.FieldObservations = nil
	if err := request.bindRequestProjection(countDocument); err != nil {
		return nil, err
	}
	for _, rule := range mapping.ConversionDefaults {
		if rule.Phase != ConversionRequest {
			return nil, fmt.Errorf("%s/conversionDefaults: counting only projects requests", path)
		}
	}
	if err := request.compileConversionDefaults(countDocument.Mapping, expressions); err != nil {
		return nil, err
	}
	return request, nil
}

// TokenCountRequestProtocol returns the immutable request programs for one
// counting operation. Its identity belongs to the same pinned package revision.
func (compiled *Compiled) TokenCountRequestProtocol(operation string) (*Compiled, bool) {
	if compiled.packagePlan == nil {
		return nil, false
	}
	count := compiled.packagePlan.tokenCounts[operation]
	if count == nil {
		return nil, false
	}
	return count.request, true
}

func (compiled *Compiled) tokenCounting(operation string) (*compiledTokenCounting, error) {
	if compiled.packagePlan != nil {
		if count := compiled.packagePlan.tokenCounts[operation]; count != nil {
			return count, nil
		}
	}
	return nil, operationMetadataError("/operations/"+escapePointer(operation), "operation has no declared token counter")
}

// DecodeTokenCount validates the provider response before returning its count.
func (compiled *Compiled) DecodeTokenCount(ctx context.Context, operation string, input Value) (TokenCount, error) {
	count, err := compiled.tokenCounting(operation)
	if err != nil {
		return TokenCount{}, err
	}
	path := "/mapping/operations/" + escapePointer(operation) + "/countTokens/decodeResponse"
	output, err := compiled.executePackageOperationPlan(ctx, count.decode, DecodeResponse, path, input, Value{raw: "null"}, count.responseContract, compiledWireContract{}, EvaluationContext{})
	if err != nil {
		return TokenCount{}, err
	}
	result, err := count.readResult(output, compiled.limits)
	return result, compiled.runtimeError(DecodeResponse, err)
}

// EncodeTokenCount renders the count through the client's declared response
// mapping and final wire contract, without fabricating generation usage.
func (compiled *Compiled) EncodeTokenCount(ctx context.Context, operation string, result TokenCount) (Value, error) {
	count, err := compiled.tokenCounting(operation)
	if err != nil {
		return Value{}, err
	}
	if result.InputTokens < 0 {
		return Value{}, compiled.mappingError(EncodeResponse, fmt.Errorf("token count must be nonnegative"))
	}
	input, err := EncodeValue(result)
	if err != nil {
		return Value{}, err
	}
	path := "/mapping/operations/" + escapePointer(operation) + "/countTokens/encodeResponse"
	return compiled.executePackageOperationPlan(ctx, count.encode, EncodeResponse, path, input, Value{raw: "null"}, compiledWireContract{}, count.responseContract, EvaluationContext{})
}

func (count *compiledTokenCounting) readResult(input Value, limits Limits) (TokenCount, error) {
	var result TokenCount
	if err := count.resultValidator.validate(input, "/tokenCount", limits); err != nil {
		return result, err
	}
	if err := decodeContract(input.Bytes(), &result); err != nil {
		return result, err
	}
	if result.InputTokens < 0 {
		return result, fmt.Errorf("token count must be nonnegative")
	}
	return result, nil
}
