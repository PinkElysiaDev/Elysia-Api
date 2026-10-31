package protocol

import (
	"strings"
	"testing"
)

func TestPackageDocumentsShareCompiledContract(t *testing.T) {
	document := textProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeRequest]
	entry.Document = document.Mapping.Directions[DecodeRequest].Document
	document.Mapping.Directions[EncodeRequest] = entry
	operation := document.Spec.Operations["generate"]
	operation.Request = entry.Document
	document.Spec.Operations["generate"] = operation
	compiled := compilePackageForTest(t, document)
	if compiled.wireContracts[DecodeRequest].validator != compiled.wireContracts[EncodeRequest].validator {
		t.Fatal("one document created separate validators")
	}
	bound, exists := compiled.Operation("generate")
	if !exists || bound.Request != EncodeRequest || bound.Response != DecodeResponse {
		t.Fatal("wire operation lost its explicit mapping binding", bound)
	}
	streamDocument := eventProtocolPackage(t)
	entry = streamDocument.Mapping.Directions[EncodeEvent]
	entry.Document = streamDocument.Mapping.Directions[DecodeEvent].Document
	streamDocument.Mapping.Directions[EncodeEvent] = entry
	stream := compilePackageForTest(t, streamDocument)
	input, output := stream.wireContracts[DecodeEvent], stream.wireContracts[EncodeEvent]
	if input.validator != output.validator || input.packageStream != output.packageStream {
		t.Fatal("one event document created separate immutable contracts")
	}
}

func TestPackageDocumentsValidateWithoutMappingReferences(t *testing.T) {
	document := eventProtocolPackage(t)
	delete(document.Mapping.Directions, DecodeEvent)
	delete(document.Mapping.Directions, EncodeEvent)
	compilePackageForTest(t, document)
	document.Spec.Documents["unused-invalid"] = WireContractSpec{}
	if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), "/spec/documents/unused-invalid/schema") {
		t.Fatal("unmapped document escaped declaration validation", err)
	}
}

func TestPackageDocumentIdentityFollowsReferences(t *testing.T) {
	document := eventProtocolPackage(t)
	before := compilePackageForTest(t, document)
	contract := document.Spec.Documents["provider-event"]
	lifecycle := *contract.Stream
	lifecycle.Sequence = "/other_seq"
	contract.Stream = &lifecycle
	document.Spec.Documents["provider-event"] = contract
	after := compilePackageForTest(t, document)
	if before.Hash() == after.Hash() || before.mappings[DecodeEvent].definitionHash == after.mappings[DecodeEvent].definitionHash {
		t.Fatal("referenced document was omitted from identity")
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse, EncodeEvent} {
		if before.mappings[direction].definitionHash != after.mappings[direction].definitionHash {
			t.Fatal("unrelated document changed mapping identity", direction)
		}
	}
}

func TestPackageDirectionIdentityIncludesSpecReferences(t *testing.T) {
	document := textProtocolPackage(t)
	document.Spec.Expressions = map[string]Expression{
		"outer": {Op: "ref", Ref: "inner"},
		"inner": literalExpression(true),
	}
	contract := WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}, Assertions: []WireAssertion{{
		Path: "/guarded", Schema: &ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "declared check",
		When: &Expression{Op: "ref", Ref: "outer"},
	}}}
	document.Spec.Documents["provider-response"] = contract
	document.Spec.Documents["checked-final"] = contract
	entry := document.Mapping.Directions[EncodeResponse]
	entry.FinalDocument = "checked-final"
	document.Mapping.Directions[EncodeResponse] = entry
	before := compilePackageForTest(t, document)
	document.Spec.Expressions["inner"] = literalExpression(false)
	after := compilePackageForTest(t, document)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
		isChanged := before.mappings[direction].definitionHash != after.mappings[direction].definitionHash
		usesReference := direction == DecodeResponse || direction == EncodeResponse
		if isChanged != usesReference {
			t.Fatal("direction identity lost its own spec dependency or included an unrelated document", direction, isChanged)
		}
	}
}

func TestPackageFinalDocumentIdentityAndBinding(t *testing.T) {
	contract := WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}}
	for _, direction := range []Direction{EncodeRequest, EncodeResponse} {
		t.Run(string(direction), func(t *testing.T) {
			document := textProtocolPackage(t)
			document.Spec.Documents["delivered-output"] = contract
			entry := document.Mapping.Directions[direction]
			entry.FinalDocument = "delivered-output"
			document.Mapping.Directions[direction] = entry
			before := compilePackageForTest(t, document)
			changed := contract
			changed.Assertions = []WireAssertion{{Path: "/required", Schema: &ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: "required field must be a string"}}
			document.Spec.Documents["delivered-output"] = changed
			after := compilePackageForTest(t, document)
			if before.Hash() == after.Hash() || before.mappings[direction].definitionHash == after.mappings[direction].definitionHash {
				t.Fatal("final contract was omitted from protocol or mapping identity")
			}
			for _, inputDirection := range []Direction{DecodeRequest, DecodeResponse} {
				if before.mappings[inputDirection].definitionHash != after.mappings[inputDirection].definitionHash {
					t.Fatal("final-only document changed source decoding identity", inputDirection)
				}
			}
		})
	}
	for _, fixture := range []struct {
		direction Direction
		name      string
	}{{DecodeRequest, "delivered-response"}, {DecodeResponse, "delivered-response"}, {EncodeResponse, "missing"}} {
		candidate := textProtocolPackage(t)
		candidate.Spec.Documents["delivered-response"] = contract
		entry := candidate.Mapping.Directions[fixture.direction]
		entry.FinalDocument = fixture.name
		candidate.Mapping.Directions[fixture.direction] = entry
		if err := candidate.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), "/finalDocument") {
			t.Fatal("unsupported final binding was accepted", fixture, err)
		}
	}
}

func TestPackageOperationBindings(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolPackage)
		path string
	}{
		{"missing-binding", func(p *ProtocolPackage) { delete(p.Mapping.Operations, "generate") }, "/mapping/operations/generate"},
		{"unknown-operation", func(p *ProtocolPackage) { p.Mapping.Operations["missing"] = ProtocolOperationMapping{} }, "/mapping/operations/missing"},
		{"missing-document", func(p *ProtocolPackage) {
			op := p.Spec.Operations["generate"]
			op.Request = "missing"
			p.Spec.Operations["generate"] = op
		}, "/spec/operations/generate/request"},
		{"wrong-document", func(p *ProtocolPackage) {
			binding := p.Mapping.Operations["generate"]
			binding.Request = DecodeRequest
			p.Mapping.Operations["generate"] = binding
		}, "/mapping/operations/generate/request"},
		{"wrong-direction", func(p *ProtocolPackage) {
			op := p.Spec.Operations["generate"]
			op.Request = "provider-response"
			p.Spec.Operations["generate"] = op
			binding := p.Mapping.Operations["generate"]
			binding.Request = DecodeResponse
			p.Mapping.Operations["generate"] = binding
		}, "/mapping/operations/generate/request"},
		{"stream-without-lifecycle", func(p *ProtocolPackage) {
			op := p.Spec.Operations["generate"]
			op.Transport = SSE
			p.Spec.Operations["generate"] = op
		}, "/spec/operations/generate/response"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			fixture.edit(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), fixture.path) {
				t.Fatal("invalid operation binding was accepted or misattributed", err)
			}
		})
	}
}

func TestPackageRejectsLegacySpecFields(t *testing.T) {
	raw := fixtureValue(t, textProtocolPackage(t)).JSONString()
	for _, input := range []string{
		strings.Replace(raw, `"documents":`, `"wireContracts":`, 1),
		strings.Replace(raw, `"spec":{`, `"spec":{"stream":{},`, 1),
		strings.Replace(raw, `"kind":"generate"`, `"task":{"decode":{"module":"anthropic"}},"kind":"generate"`, 1),
		strings.Replace(raw, `"kind":"generate"`, `"models":{"decode":{"module":"anthropic"}},"kind":"generate"`, 1),
		strings.Replace(raw, `"kind":"generate"`, `"session":{},"kind":"generate"`, 1),
		strings.Replace(raw, `"request":"generation-request"`, `"request":"encode_request"`, 1),
	} {
		if input == raw {
			t.Fatal("legacy rejection fixture did not change the package")
		}
		if _, err := DecodeProtocolPackage([]byte(input), DefaultLimits()); err == nil {
			t.Fatal("legacy spec syntax remained accepted")
		}
	}
}
