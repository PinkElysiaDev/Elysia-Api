package protocol

import (
	"fmt"
	"slices"
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

// ProtocolStreamSpec declares one response's wire lifecycle independently of
// its mapping. Both event directions use these selectors and wire tag meanings.
// Sequence, when declared, selects strictly increasing nonnegative int64 JSON
// integer literals; decimal and exponent spellings are not sequence integers.
type ProtocolStreamSpec struct {
	Discriminator string                             `json:"discriminator"`
	ResponseID    string                             `json:"responseId"`
	ItemID        string                             `json:"itemId"`
	Sequence      string                             `json:"sequence,omitempty"`
	Events        map[string]ProtocolStreamEventSpec `json:"events"`
}

// ProtocolStreamEventSpec lists the ordered actions carried by one wire frame.
// Sequence numbers belong to the frame and are checked once before its actions.
type ProtocolStreamEventSpec []ProtocolStreamActionSpec

// ProtocolStreamActionSpec selects an explicit text or JSON-tool lifecycle
// action. It does not infer starts, late identities, snapshots or controls.
// item_finish closes the established kind; tool_finish requires a tool item.
type ProtocolStreamActionSpec struct {
	Action    string                   `json:"action"`
	Text      string                   `json:"text,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
	Tool      *ProtocolStreamToolSpec  `json:"tool,omitempty"`
	Error     string                   `json:"error,omitempty"`
	Usage     *ProtocolStreamUsageSpec `json:"usage,omitempty"`
}

// ProtocolStreamToolSpec selects the complete identity required at tool_start.
// Both fields must be nonempty strings on that frame; input is always JSON.
type ProtocolStreamToolSpec struct {
	Name   string `json:"name"`
	CallID string `json:"callId"`
}

// ProtocolStreamUsageSpec selects observed cumulative token counts. Missing
// selectors omit counters; present values must be nonnegative integers. Required
// wire counters belong to the document schema or assertions. Counts use int64
// integer literals, excluding decimal and exponent spellings.
type ProtocolStreamUsageSpec struct {
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	Total  string `json:"total,omitempty"`
}

// ProtocolMapping contains only declarative programs and their semantic effects.
// A v2 module or after mapping cannot be selected through this contract.
type ProtocolMapping struct {
	CoreVersion    int                                    `json:"coreVersion"`
	DSLVersion     int                                    `json:"dslVersion"`
	Directions     map[Direction]ProtocolDirectionMapping `json:"directions"`
	Operations     map[string]ProtocolOperationMapping    `json:"operations"`
	Expressions    map[string]Expression                  `json:"expressions,omitempty"`
	Native         NativePolicy                           `json:"native"`
	Capabilities   CapabilitySet                          `json:"capabilities"`
	Limits         *Limits                                `json:"limits,omitempty"`
	Samples        []Sample                               `json:"samples"`
	ModelSamples   []ModelSample                          `json:"modelSamples,omitempty"`
	TaskSamples    []TaskSample                           `json:"taskSamples,omitempty"`
	SessionSamples []SessionSample                        `json:"sessionSamples,omitempty"`
	Agent          *ProtocolAgentMapping                  `json:"agent,omitempty"`
}

// ProtocolDirectionMapping transforms one wire/core boundary without a codec module.
type ProtocolDirectionMapping struct {
	Document     string                   `json:"document"`
	IsEventBatch bool                     `json:"eventBatch,omitempty"`
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
	Diagnostic   *RejectDiagnostic        `json:"diagnostic,omitempty"`
}

// RejectDiagnostic classifies an explicit mapping rejection. The effect still
// supplies its business path, explanation and rule identity.
type RejectDiagnostic struct {
	Code       IssueCode `json:"code"`
	Stage      string    `json:"stage"`
	Suggestion string    `json:"suggestion,omitempty"`
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
	if len(mapping.Directions) == 0 && len(mapping.Operations) == 0 {
		return nil, fmt.Errorf("/mapping: protocol package requires mapping directions or operation programs")
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
	wireSpec, err := spec.compile(limits)
	if err != nil {
		return nil, err
	}
	compiled := &Compiled{identity: spec.Identity(), limits: limits, explicitLimits: mapping.Limits != nil || limits.BufferBytes != DefaultLimits().BufferBytes, native: mapping.Native, capabilities: mapping.Capabilities, operations: wireSpec.operations, operationSchemas: wireSpec.operationSchemas, mappings: map[Direction]compiledMapping{}, wireContracts: map[Direction]compiledWireContract{}, packagePlan: &compiledPackagePlan{coreVersion: mapping.CoreVersion, dslVersion: mapping.DSLVersion, effects: map[Direction][]compiledSemanticEffect{}}}
	expressions := &expressionCompiler{limits: limits, references: mapping.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
	for _, direction := range sortedKeys(mapping.Directions) {
		if !slices.Contains(DirectionCatalog(), direction) {
			return nil, fmt.Errorf("/mapping/directions/%s: unsupported package direction", direction)
		}
		entry := mapping.Directions[direction]
		wire, exists := spec.Documents[entry.Document]
		if !exists {
			return nil, fmt.Errorf("/mapping/directions/%s/document: requires a declared wire document", direction)
		}
		contract := wireSpec.documents[entry.Document]
		if contract.packageStream != nil && !isEventDirection(direction) {
			return nil, fmt.Errorf("/mapping/directions/%s/document: stream documents require an event direction", direction)
		}
		compiled.wireContracts[direction] = contract
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
		if contract.packageStream != nil {
			if !program.capabilities[TextCapability] || !program.capabilities[UsageCapability] {
				return nil, fmt.Errorf("/mapping/directions/%s/capabilities: stream contract requires text and usage", direction)
			}
			if contract.packageStream.hasTools && !program.capabilities[FunctionToolsCapability] {
				return nil, fmt.Errorf("/mapping/directions/%s/capabilities: stream tool actions require tools.function", direction)
			}
		}
		compiled.mappings[direction], compiled.packagePlan.effects[direction] = program, effects
	}
	if err := compiled.bindPackageOperations(spec, mapping); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageModelMappings(spec, mapping, wireSpec.documents, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageTaskMappings(spec, mapping, wireSpec.documents, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageAgent(mapping.Agent, expressions); err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(mapping.Expressions) {
		if !expressions.used[name] {
			return nil, fmt.Errorf("/mapping/expressions/%s: unreachable expression", name)
		}
	}
	if err := validateProtocolSamples(mapping, compiled.operations, limits); err != nil {
		return nil, err
	}
	if err := compiled.bindPackageIdentity(document); err != nil {
		return nil, err
	}
	return compiled, nil
}

func validateProtocolSamples(mapping ProtocolMapping, operations map[string]Operation, limits Limits) error {
	count := len(mapping.Samples) + len(mapping.ModelSamples) + len(mapping.TaskSamples) + len(mapping.SessionSamples)
	if mapping.Agent != nil {
		count += len(mapping.Agent.Samples)
	}
	if count > limits.StateItems {
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
	return checkSessionSamples(mapping.SessionSamples, operations, func(direction Direction) bool {
		_, exists := mapping.Directions[direction]
		return exists
	}, limits.StateItems, seen)
}
