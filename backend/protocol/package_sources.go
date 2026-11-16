package protocol

import (
	"fmt"
	"strconv"
)

// CompileSources compiles spec.json and mapping.json as one immutable package.
// Both sources and their complete package share the engine's resource limits;
// successful compilation does not supply verification or activation evidence.
func (compiler *Compiler) CompileSources(specRaw, mappingRaw []byte) (*Compiled, []ConversionIssue) {
	// Join without normalizing either source. The cache shares exactly the same
	// byte identity as the embedded package loader, not a typed JSON re-encoding.
	prefix := `{"schemaVersion":` + strconv.Itoa(ProtocolPackageSchemaVersion) + `,"spec":`
	if err := compiler.limits.Validate(); err != nil {
		return nil, definitionIssues(Identity{}, "/", err)
	}
	// ParseValue validates syntax only; it does not build a parsed tree. Check
	// boundaries even on a cache hit: the same joined bytes must not authorize
	// an invalid split at an embedded ,"mapping": token.
	spec, err := parseProtocolSource(specRaw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(Identity{}, "/spec", err)
	}
	mapping, err := parseProtocolSource(mappingRaw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(Identity{}, "/mapping", err)
	}
	size := len(prefix) + len(specRaw) + len(`,"mapping":`) + len(mappingRaw) + 1
	if size > compiler.limits.BufferBytes {
		return nil, definitionIssues(Identity{}, "/", fmt.Errorf("protocol package exceeds buffer limit"))
	}
	raw := make([]byte, 0, size)
	raw = append(raw, prefix...)
	raw = append(raw, specRaw...)
	raw = append(raw, `,"mapping":`...)
	raw = append(raw, mappingRaw...)
	raw = append(raw, '}')
	return processCompilations.compile(compiler.compilationKey(raw), func() (*Compiled, []ConversionIssue) {
		document := ProtocolPackage{SchemaVersion: ProtocolPackageSchemaVersion}
		if err := checkProtocolSourceLimits(spec, mapping, compiler.limits); err != nil {
			return nil, definitionIssues(document.Spec.Identity(), "/", err)
		}
		// One typed decode for the whole package, after raw token metering has
		// preserved duplicate fields, authored whitespace, and extra nesting.
		if err := decodeContract(raw, &document); err != nil {
			rejected := definitionIssues(document.Spec.Identity(), "/", err)
			return nil, sourceCompilationIssues(rejected, sourceDecodeIssues(specRaw, mappingRaw))
		}
		return compiler.compilePackage(document)
	})
}

func sourceCompilationIssues(rejected, attributed []ConversionIssue) []ConversionIssue {
	if IssuesError(attributed) != nil {
		return attributed
	}
	// Diagnostic refinement is never authority to accept a rejected package.
	return rejected
}

// This cold diagnostic-only fallback preserves the established independent
// source path, identity and codec message. It can never authorize compilation.
func sourceDecodeIssues(specRaw, mappingRaw []byte) []ConversionIssue {
	var document ProtocolPackage
	if err := decodeContract(specRaw, &document.Spec); err != nil {
		return definitionIssues(document.Spec.Identity(), "/spec", err)
	}
	if err := decodeContract(mappingRaw, &document.Mapping); err != nil {
		return definitionIssues(document.Spec.Identity(), "/mapping", err)
	}
	return nil
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
