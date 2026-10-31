package protocol

import (
	"fmt"
	"slices"
)

// Document reuse adds boundary assertions to an already compiled wire shape.
// The resolved descriptions are needed only while compiling mapping programs.
type protocolDocumentCompiler struct {
	spec        ProtocolSpec
	compiled    *compiledProtocolSpec
	expressions *expressionCompiler
	visiting    map[string]bool
	depths      map[string]int
}

func (compiler *protocolDocumentCompiler) compile(name string, depth int) error {
	path := "/spec/documents/" + escapePointer(name)
	if _, exists := compiler.compiled.documents[name]; exists {
		if depth+compiler.depths[name]-1 > compiler.expressions.limits.Depth {
			return fmt.Errorf("%s/base: document reuse exceeds the depth limit", path)
		}
		return nil
	}
	document := compiler.spec.Documents[name]
	if !definitionIdentifier.MatchString(name) {
		return fmt.Errorf("%s: invalid document name", path)
	}
	if compiler.visiting[name] {
		return fmt.Errorf("%s/base: document reuse contains a cycle", path)
	}
	if depth > compiler.expressions.limits.Depth {
		return fmt.Errorf("%s/base: document reuse exceeds the depth limit", path)
	}
	compiler.visiting[name] = true
	defer delete(compiler.visiting, name)
	if document.Base != "" {
		if document.Schema != nil || document.Stream != nil {
			return fmt.Errorf("%s/base: a reused document may only append assertions", path)
		}
		if _, exists := compiler.spec.Documents[document.Base]; !exists {
			return fmt.Errorf("%s/base: requires a declared wire document", path)
		}
		if err := compiler.compile(document.Base, depth+1); err != nil {
			return err
		}
	}

	references := map[string]Expression{}
	compiler.expressions.mappingReferences = references
	contract, resolved, err := compiler.compileContract(document, path)
	compiler.expressions.mappingReferences = nil
	if err != nil {
		return err
	}
	identity, err := EncodeValue(struct {
		Document   WireContractSpec      `json:"document"`
		BaseHash   string                `json:"baseHash,omitempty"`
		References map[string]Expression `json:"references"`
	}{document, compiler.compiled.documentHashes[document.Base], references})
	if err != nil {
		return err
	}
	compiler.compiled.documents[name] = contract
	compiler.compiled.resolvedDocuments[name] = resolved
	compiler.compiled.documentHashes[name] = hashValue(identity)
	compiler.depths[name] = 1 + compiler.depths[document.Base]
	return nil
}

func (compiler *protocolDocumentCompiler) compileContract(document WireContractSpec, path string) (compiledWireContract, WireContractSpec, error) {
	if document.Base == "" {
		contract, err := compiler.expressions.compileWireContractSpec(document, path)
		return contract, document, err
	}
	contract := compiler.compiled.documents[document.Base]
	resolved := compiler.compiled.resolvedDocuments[document.Base]
	if len(document.Assertions) == 0 {
		return contract, resolved, nil
	}
	assertions, err := compileWireAssertions(document.Assertions, path+"/assertions", resolved.Schema, resolved.Schema, compiler.expressions, 1)
	if err != nil {
		return compiledWireContract{}, WireContractSpec{}, err
	}
	// Every Spec document has this validator; module validators never enter this compiler.
	base := contract.validator.(*specWireValidator)
	validator := &specWireValidator{schema: base.schema, limits: base.limits,
		assertions: append(slices.Clone(base.assertions), assertions...)}
	contract.validator, contract.contextValidator = validator, validator
	resolved.Assertions = append(slices.Clone(resolved.Assertions), document.Assertions...)
	return contract, resolved, nil
}

// resolvedDocumentSchema follows a chain already checked by compileContract.
// Only compilation proofs use the authored descriptions; requests use plans.
func (spec ProtocolSpec) resolvedDocumentSchema(name string) *ValueSchema {
	document := spec.Documents[name]
	for document.Base != "" {
		document = spec.Documents[document.Base]
	}
	return document.Schema
}
