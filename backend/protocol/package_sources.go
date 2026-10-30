package protocol

import (
	"fmt"
	"strconv"
)

// CompileSources compiles spec.json and mapping.json as one immutable package.
// Both sources and their complete package share the engine's resource limits;
// successful compilation does not supply verification or activation evidence.
func (compiler *Compiler) CompileSources(specRaw, mappingRaw []byte) (*Compiled, []ConversionIssue) {
	document := ProtocolPackage{SchemaVersion: ProtocolPackageSchemaVersion}
	if err := compiler.limits.Validate(); err != nil {
		return nil, definitionIssues(Identity{}, "/", err)
	}
	spec, err := parseProtocolSource(specRaw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/spec", err)
	}
	mapping, err := parseProtocolSource(mappingRaw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/mapping", err)
	}
	if err := checkProtocolSourceLimits(spec, mapping, compiler.limits); err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/", err)
	}
	if err := decodeContract(specRaw, &document.Spec); err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/spec", err)
	}
	if err := decodeContract(mappingRaw, &document.Mapping); err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/mapping", err)
	}
	return compiler.compilePackage(document)
}

func parseProtocolSource(raw []byte, limits Limits) (Value, error) {
	if len(raw) > limits.BufferBytes {
		return Value{}, fmt.Errorf("protocol source exceeds buffer limit")
	}
	return ParseValue(raw)
}

func checkProtocolSourceLimits(spec, mapping Value, limits Limits) error {
	// Count the authored bytes and tokens, including duplicate keys and the
	// package's extra nesting, before typed encoding can normalize them away.
	parts := [...]string{`{"schemaVersion":` + strconv.Itoa(ProtocolPackageSchemaVersion) + `,"spec":`, spec.raw, `,"mapping":`, mapping.raw, `}`}
	bytes := 0
	for _, part := range parts {
		bytes += len(part)
	}
	if bytes > limits.BufferBytes {
		return fmt.Errorf("protocol package exceeds buffer limit")
	}
	meter := valueTokenMeter{limits: limits, domain: "wire"}
	for _, part := range parts {
		if err := meter.scan(part); err != nil {
			return err
		}
	}
	return nil
}
