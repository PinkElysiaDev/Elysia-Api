package protocol

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// ProtocolPackageSchemaVersion identifies the independent spec/mapping format.
	ProtocolPackageSchemaVersion = 3
	// ProtocolCoreVersion identifies the core contract targeted by v3 mappings.
	ProtocolCoreVersion = 2
	// ProtocolDSLVersion identifies the declarative expression and effect language.
	ProtocolDSLVersion = 1
)

// ProtocolPackage binds a wire contract to an explicitly authored core mapping.
// Parsing a package does not activate it or supply verification evidence.
type ProtocolPackage struct {
	SchemaVersion int             `json:"schemaVersion"`
	Spec          ProtocolSpec    `json:"spec"`
	Mapping       ProtocolMapping `json:"mapping"`
}

// ProtocolSpec describes the provider's wire identity, endpoints and documents.
// WireContracts describe input on decode directions and output on encode directions.
type ProtocolSpec struct {
	ID            string                         `json:"id"`
	Name          string                         `json:"name"`
	Version       string                         `json:"version"`
	Family        string                         `json:"family"`
	WireVersion   string                         `json:"wireVersion"`
	Operations    map[string]Operation           `json:"operations"`
	WireContracts map[Direction]WireContractSpec `json:"wireContracts"`
}

// Identity returns the authored wire identity; content hashes bind its execution.
func (spec ProtocolSpec) Identity() Identity {
	return Identity{DefinitionID: spec.ID, Revision: spec.Version, Family: spec.Family, WireVersion: spec.WireVersion}
}

// ProtocolMapping contains only declarative programs and their semantic effects.
// A v2 module or after mapping cannot be selected through this contract.
type ProtocolMapping struct {
	CoreVersion  int                                    `json:"coreVersion"`
	DSLVersion   int                                    `json:"dslVersion"`
	Directions   map[Direction]ProtocolDirectionMapping `json:"directions"`
	Expressions  map[string]Expression                  `json:"expressions,omitempty"`
	Native       NativePolicy                           `json:"native"`
	Capabilities CapabilitySet                          `json:"capabilities"`
	Limits       *Limits                                `json:"limits,omitempty"`
	Samples      []Sample                               `json:"samples"`
}

// ProtocolDirectionMapping transforms one wire/core boundary without a codec module.
type ProtocolDirectionMapping struct {
	Program      Expression               `json:"program"`
	Effects      []ProtocolSemanticEffect `json:"effects"`
	Capabilities CapabilitySet            `json:"capabilities,omitzero"`
}

// ProtocolSemanticEffect declares how a program handles a core semantic location.
// Path always addresses core data and Target addresses wire data. When observes
// the direction's program input, or one CacheIntent for cache_intents scope.
// Declarations alone do not prove fidelity.
type ProtocolSemanticEffect struct {
	ID           string                   `json:"id"`
	Kind         string                   `json:"kind"`
	Path         string                   `json:"path"`
	Target       string                   `json:"target,omitempty"`
	When         *Expression              `json:"when,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
	Layout       *ProtocolAggregateLayout `json:"layout,omitempty"`
	ValueMapping *ProtocolValueMapping    `json:"valueMapping,omitempty"`
	Scope        string                   `json:"scope,omitempty"`
	Default      Value                    `json:"default,omitzero"`
	Severity     Severity                 `json:"severity,omitempty"`
}

// ProtocolAggregateLayout partitions one wire object into a core owner.
// Fields maps wire member names to independent core member names; Remainder
// retains every other wire member. Labels identifies the closed core variant.
type ProtocolAggregateLayout struct {
	Remainder string            `json:"remainder"`
	Fields    map[string]string `json:"fields"`
	Labels    Object            `json:"labels"`
}

// Parsing is separate so CompilePackage can retain the single compilation's
// programs and validators instead of repeating validation after Decode.
func decodeProtocolPackage(raw []byte, limits Limits) (ProtocolPackage, error) {
	var document ProtocolPackage
	if err := limits.Validate(); err != nil {
		return document, err
	}
	if len(raw) > limits.BufferBytes {
		return document, fmt.Errorf("protocol package exceeds buffer limit")
	}
	value, err := ParseValue(raw)
	if err != nil {
		return document, err
	}
	if err := checkValueLimits(value, limits); err != nil {
		return document, err
	}
	if err := decodeContract(raw, &document); err != nil {
		return document, err
	}
	return document, nil
}

func (document ProtocolPackage) compileContract(engineLimits Limits) (*Compiled, error) {
	if err := engineLimits.Validate(); err != nil {
		return nil, err
	}
	if document.SchemaVersion != ProtocolPackageSchemaVersion {
		return nil, fmt.Errorf("/schemaVersion: expected %d", ProtocolPackageSchemaVersion)
	}
	if document.Mapping.CoreVersion != ProtocolCoreVersion || document.Mapping.DSLVersion != ProtocolDSLVersion {
		return nil, fmt.Errorf("/mapping: expected coreVersion %d and dslVersion %d", ProtocolCoreVersion, ProtocolDSLVersion)
	}
	spec, mapping := document.Spec, document.Mapping
	if !definitionIdentifier.MatchString(spec.ID) || strings.TrimSpace(spec.Version) == "" || strings.TrimSpace(spec.Family) == "" || strings.TrimSpace(spec.WireVersion) == "" {
		return nil, fmt.Errorf("/spec: id, version, family and wireVersion are required")
	}
	if len(mapping.Directions) == 0 || len(spec.Operations) == 0 {
		return nil, fmt.Errorf("protocol package requires directions and operations")
	}
	limits := engineLimits
	if mapping.Limits != nil {
		limits = *mapping.Limits
		if err := limits.Validate(); err != nil {
			return nil, err
		}
		if limits.Depth > engineLimits.Depth || limits.Nodes > engineLimits.Nodes || limits.Mutations > engineLimits.Mutations || limits.StateItems > engineLimits.StateItems || limits.BufferBytes > engineLimits.BufferBytes {
			return nil, fmt.Errorf("/mapping/limits: cannot exceed engine limits")
		}
	}
	for _, capability := range sortedKeys(mapping.Capabilities) {
		if !slices.Contains(CapabilityCatalog(), capability) {
			return nil, fmt.Errorf("/mapping/capabilities: unknown capability %q", capability)
		}
	}
	for _, path := range sortedKeys(mapping.Native.ArrayKeys) {
		if !mapping.Native.Preserve {
			return nil, fmt.Errorf("/mapping/native: arrayKeys requires preserve")
		}
		for _, pointer := range []string{path, mapping.Native.ArrayKeys[path]} {
			if _, err := parsePointer(pointer); err != nil {
				return nil, fmt.Errorf("/mapping/native/arrayKeys: %w", err)
			}
		}
	}
	for _, direction := range sortedKeys(spec.WireContracts) {
		if _, exists := mapping.Directions[direction]; !exists {
			return nil, fmt.Errorf("/spec/wireContracts/%s: missing mapping direction", direction)
		}
	}
	compiled := &Compiled{identity: spec.Identity(), limits: limits, explicitLimits: mapping.Limits != nil || limits.BufferBytes != DefaultLimits().BufferBytes, native: mapping.Native, capabilities: mapping.Capabilities, operations: spec.Operations, operationSchemas: map[string]*runtimeValueSchema{}, mappings: map[Direction]compiledMapping{}, wireContracts: map[Direction]compiledWireContract{}, packagePlan: &compiledPackagePlan{coreVersion: mapping.CoreVersion, dslVersion: mapping.DSLVersion, effects: map[Direction][]compiledSemanticEffect{}}}
	expressions := &expressionCompiler{limits: limits, references: mapping.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
	for _, direction := range sortedKeys(mapping.Directions) {
		if !slices.Contains([]Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse, DecodeEvent, EncodeEvent}, direction) {
			return nil, fmt.Errorf("/mapping/directions/%s: unsupported package direction", direction)
		}
		wire, exists := spec.WireContracts[direction]
		if !exists {
			return nil, fmt.Errorf("/spec/wireContracts/%s: wire contract is required", direction)
		}
		contract, err := compileWireContractSpec(wire, "/spec/wireContracts/"+string(direction), limits)
		if err != nil {
			return nil, err
		}
		compiled.wireContracts[direction] = contract
		entry := mapping.Directions[direction]
		for _, capability := range sortedKeys(entry.Capabilities) {
			if !mapping.Capabilities[capability] {
				return nil, fmt.Errorf("/mapping/directions/%s/capabilities: undeclared capability %q", direction, capability)
			}
		}
		program, effects, err := compileProtocolProgram(entry, direction, wire, expressions, mapping.CoreVersion, mapping.DSLVersion)
		if err != nil {
			return nil, err
		}
		program.capabilities = mapping.Capabilities
		if entry.Capabilities != nil {
			program.capabilities = entry.Capabilities
		}
		compiled.mappings[direction], compiled.packagePlan.effects[direction] = program, effects
	}
	for _, name := range sortedKeys(mapping.Expressions) {
		if !expressions.used[name] {
			return nil, fmt.Errorf("/mapping/expressions/%s: unreachable expression", name)
		}
	}
	// Reuse endpoint validation only. This reference set contains no executable
	// legacy mapping and is never compiled or serialized as a v2 definition.
	references := Definition{Operations: spec.Operations, Capabilities: mapping.Capabilities, Directions: map[Direction]Mapping{}}
	for direction := range mapping.Directions {
		references.Directions[direction] = Mapping{}
	}
	validator := &Compiler{limits: limits}
	for _, name := range sortedKeys(spec.Operations) {
		operation := spec.Operations[name]
		if operation.Kind != "generate" || operation.Task != nil || operation.Models != nil || operation.Session != nil || operation.Transport == WebSocket {
			return nil, fmt.Errorf("/spec/operations/%s: task, model discovery and session programs are not supported by this package compiler", name)
		}
		if err := validator.checkOperation(name, operation, references); err != nil {
			return nil, fmt.Errorf("/spec/operations/%s: %w", name, err)
		}
	}
	if err := validateProtocolSamples(mapping, spec.Operations, limits); err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(spec.Operations) {
		schema, err := compileValueSchema(spec.Operations[name].Input, limits)
		if err != nil {
			return nil, err
		}
		compiled.operationSchemas[name] = schema
	}
	if err := compiled.bindPackageIdentity(document); err != nil {
		return nil, err
	}
	return compiled, nil
}

func validateProtocolSamples(mapping ProtocolMapping, operations map[string]Operation, limits Limits) error {
	if len(mapping.Samples) > limits.StateItems {
		return fmt.Errorf("/mapping/samples: sample count exceeds fixture limit")
	}
	seen := map[string]bool{}
	for index, sample := range mapping.Samples {
		at := fmt.Sprintf("/mapping/samples/%d", index)
		if sample.ID == "" || seen[sample.ID] {
			return fmt.Errorf("%s/id: expected a unique nonempty sample ID", at)
		}
		seen[sample.ID] = true
		if _, exists := mapping.Directions[sample.Direction]; !exists {
			return fmt.Errorf("%s/direction: missing mapping direction", at)
		}
		if sample.Operation != "" {
			if _, exists := operations[sample.Operation]; !exists || (sample.Direction != DecodeRequest && sample.Direction != EncodeRequest) {
				return fmt.Errorf("%s/operation: requires a declared generation operation and a request direction", at)
			}
		}
		if sample.Input.IsZero() || (sample.Expected.IsZero() == (sample.ExpectedIssue == "")) {
			return fmt.Errorf("%s: requires input and expected output or issue", at)
		}
		if sample.Sequence && !isEventDirection(sample.Direction) {
			return fmt.Errorf("%s/sequence: requires an event direction", at)
		}
		for _, capability := range sample.Capabilities {
			if !mapping.Capabilities[capability] {
				return fmt.Errorf("%s/capabilities: undeclared capability %q", at, capability)
			}
		}
	}
	return nil
}
