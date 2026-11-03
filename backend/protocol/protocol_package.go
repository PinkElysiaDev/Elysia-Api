package protocol

import (
	"fmt"
	"slices"
)

const (
	// ProtocolPackageSchemaVersion identifies the independent spec/mapping format.
	ProtocolPackageSchemaVersion = 4
	// ProtocolCoreVersion identifies the core contract targeted by package mappings.
	ProtocolCoreVersion = 2
	// ProtocolDSLVersion identifies the declarative expression and effect language.
	ProtocolDSLVersion = 2
)

// ProtocolPackage binds a wire contract to an explicitly authored core mapping.
// Parsing a package does not activate it or supply verification evidence.
type ProtocolPackage struct {
	SchemaVersion int             `json:"schemaVersion"`
	Spec          ProtocolSpec    `json:"spec"`
	Mapping       ProtocolMapping `json:"mapping"`
}

// ProtocolStreamSpec describes external entities and lifecycle, independently of core mappings.
type ProtocolStreamSpec struct {
	Discriminator      string                                 `json:"discriminator"`
	DiscriminatorValue *Expression                            `json:"discriminatorValue,omitempty"`
	ResponseID         string                                 `json:"responseId"`
	ResponseIDValue    *Expression                            `json:"responseIdValue,omitempty"`
	InheritResponseID  bool                                   `json:"inheritResponseId,omitempty"`
	OptionalResponseID bool                                   `json:"optionalResponseId,omitempty"`
	Sequence           string                                 `json:"sequence,omitempty"`
	OptionalSequence   bool                                   `json:"optionalSequence,omitempty"`
	SequenceDiagnostic *RejectDiagnostic                      `json:"sequenceDiagnostic,omitempty"`
	SequenceReason     string                                 `json:"sequenceReason,omitempty"`
	Namespaces         map[string]ProtocolStreamNamespaceSpec `json:"namespaces,omitempty"`
	Entities           map[string]ProtocolStreamEntitySpec    `json:"entities,omitempty"`
	Lifecycle          ProtocolStreamLifecycleSpec            `json:"lifecycle"`
	Identities         []ProtocolStreamIdentitySpec           `json:"identities,omitempty"`
	State              map[string]ProtocolWireStateSlot       `json:"state,omitempty"`
	Events             map[string]ProtocolStreamEventSpec     `json:"events"`
}

// ProtocolStreamNamespaceSpec shares keys across externally named entity variants.
type ProtocolStreamNamespaceSpec struct {
	KeyType JSONType `json:"keyType"`
}

// ProtocolStreamEntitySpec declares external payload and relation constraints.
type ProtocolStreamEntitySpec struct {
	Namespace       string                                      `json:"namespace"`
	Payload         string                                      `json:"payload"`
	CloseWithParent bool                                        `json:"closeWithParent,omitempty"`
	PayloadSchema   *ValueSchema                                `json:"payloadSchema,omitempty"`
	ParentEntities  []string                                    `json:"parentEntities,omitempty"`
	ParentNamespace string                                      `json:"parentNamespace,omitempty"`
	Identities      map[string]ProtocolStreamEntityIdentitySpec `json:"identities,omitempty"`
}

// ProtocolStreamEntityIdentitySpec fixes a supplied identity by start or finish.
type ProtocolStreamEntityIdentitySpec struct {
	Type       JSONType `json:"type"`
	RequiredAt string   `json:"requiredAt"`
	Comparison string   `json:"comparison,omitempty"`
	Unique     bool     `json:"unique,omitempty"`
	AllowEmpty bool     `json:"allowEmpty,omitempty"`
}

// ProtocolStreamLifecycleSpec makes terminal and unfinished-entity handling explicit.
type ProtocolStreamLifecycleSpec struct {
	EOF          string `json:"eof"`
	OpenEntities string `json:"openEntities"`
}

// ProtocolStreamValueSpec selects a value while retaining its external diagnostic path.
type ProtocolStreamValueSpec struct {
	Path  string      `json:"path"`
	Value *Expression `json:"value,omitempty"`
}

// ProtocolStreamIdentitySpec binds frame identities without interpreting their semantics.
type ProtocolStreamIdentitySpec struct {
	Path       string            `json:"path"`
	Type       JSONType          `json:"type"`
	When       *Expression       `json:"when,omitempty"`
	Value      *Expression       `json:"value,omitempty"`
	Key        *Expression       `json:"key,omitempty"`
	KeyPath    string            `json:"keyPath,omitempty"`
	Comparison string            `json:"comparison,omitempty"`
	Unique     bool              `json:"unique,omitempty"`
	Diagnostic *RejectDiagnostic `json:"diagnostic,omitempty"`
}

// ProtocolStreamEventSpec applies ordered actions to one immutable external frame.
type ProtocolStreamEventSpec []ProtocolStreamActionSpec

// ProtocolStreamActionSpec observes an external lifecycle or updates declared state.
type ProtocolStreamActionSpec struct {
	Action              string                             `json:"action,omitempty"`
	ForEach             string                             `json:"foreach,omitempty"`
	As                  string                             `json:"as,omitempty"`
	IndexAs             string                             `json:"indexAs,omitempty"`
	Actions             []ProtocolStreamActionSpec         `json:"actions,omitempty"`
	Assertions          []WireAssertion                    `json:"assertions,omitempty"`
	Updates             map[string]Expression              `json:"updates,omitempty"`
	When                *Expression                        `json:"when,omitempty"`
	ResponseID          string                             `json:"responseId,omitempty"`
	Entity              string                             `json:"entity,omitempty"`
	Namespace           string                             `json:"namespace,omitempty"`
	Key                 *ProtocolStreamValueSpec           `json:"key,omitempty"`
	Parent              *ProtocolStreamValueSpec           `json:"parent,omitempty"`
	Offset              *ProtocolStreamValueSpec           `json:"offset,omitempty"`
	Ordinal             *ProtocolStreamValueSpec           `json:"ordinal,omitempty"`
	Payload             *ProtocolStreamValueSpec           `json:"payload,omitempty"`
	Encoding            string                             `json:"encoding,omitempty"`
	Identities          map[string]ProtocolStreamValueSpec `json:"identities,omitempty"`
	Properties          *ProtocolStreamValueSpec           `json:"properties,omitempty"`
	ChildrenCount       *ProtocolStreamValueSpec           `json:"childrenCount,omitempty"`
	ChildrenNamespace   string                             `json:"childrenNamespace,omitempty"`
	SealPayload         bool                               `json:"sealPayload,omitempty"`
	Placeholder         bool                               `json:"placeholder,omitempty"`
	VerifyChildren      []string                           `json:"verifyChildren,omitempty"`
	VerifyCompleted     bool                               `json:"verifyCompleted,omitempty"`
	RequireVerified     []string                           `json:"requireVerified,omitempty"`
	Implicit            bool                               `json:"implicit,omitempty"`
	AllowBeforeStart    bool                               `json:"allowBeforeStart,omitempty"`
	ResponseIndependent bool                               `json:"responseIndependent,omitempty"`
	AfterTerminal       bool                               `json:"afterTerminal,omitempty"`
	FrameClass          string                             `json:"frameClass,omitempty"`
	Error               string                             `json:"error,omitempty"`
	Diagnostic          *RejectDiagnostic                  `json:"diagnostic,omitempty"`
}

// ProtocolMapping contains only declarative programs and their semantic effects.
// A v2 module or after mapping cannot be selected through this contract.
type ProtocolMapping struct {
	Continuation       *ProtocolContinuationMapping           `json:"continuation,omitempty"`
	ConversionDefaults []ProtocolConversionDefault            `json:"conversionDefaults,omitempty"`
	FieldObservations  map[string]ProtocolFieldObservation    `json:"fieldObservations,omitempty"`
	CoreVersion        int                                    `json:"coreVersion"`
	DSLVersion         int                                    `json:"dslVersion"`
	Directions         map[Direction]ProtocolDirectionMapping `json:"directions"`
	Operations         map[string]ProtocolOperationMapping    `json:"operations"`
	Expressions        map[string]Expression                  `json:"expressions,omitempty"`
	Native             NativePolicy                           `json:"native"`
	Capabilities       CapabilitySet                          `json:"capabilities"`
	Limits             *Limits                                `json:"limits,omitempty"`
	Samples            []Sample                               `json:"samples"`
	ModelSamples       []ModelSample                          `json:"modelSamples,omitempty"`
	TokenCountSamples  []Sample                               `json:"tokenCountSamples,omitempty"`
	TaskSamples        []TaskSample                           `json:"taskSamples,omitempty"`
	SessionSamples     []SessionSample                        `json:"sessionSamples,omitempty"`
	Agent              *ProtocolAgentMapping                  `json:"agent,omitempty"`
	ModelPolicy        *ProtocolModelPolicy                   `json:"modelPolicy,omitempty"`
}

// ProtocolDirectionMapping transforms one wire/core boundary without a codec module.
type ProtocolDirectionMapping struct {
	NativeReplay string `json:"nativeReplay,omitempty"`
	// NativeWhen selects replay using the wire, client intent and the read-only
	// runtime.encoding.hasRendered fact. It cannot mutate encoding or delivery.
	NativeWhen *Expression `json:"nativeWhen,omitempty"`
	Document   string      `json:"document"`
	// FinalDocument optionally selects a separate delivery contract for request,
	// response or event output. The rendering document still validates its output.
	// Document continues to constrain construction and native reconciliation.
	FinalDocument string `json:"finalDocument,omitempty"`
	// FinalCheck observes the final request wire and caller intent after all edits.
	// It cannot mutate either input or access stream state.
	FinalCheck     *Expression             `json:"finalCheck,omitempty"`
	DeliveryRanges []ProtocolDeliveryRange `json:"deliveryRanges,omitempty"`
	// SemanticRanges associates core response/event text and references before
	// encoding. Selectors read only identities, content and owned metadata.
	SemanticRanges []ProtocolDeliveryRange       `json:"semanticRanges,omitempty"`
	IsEventBatch   bool                          `json:"eventBatch,omitempty"`
	CanDeferEvents bool                          `json:"deferEvents,omitempty"`
	IsFrameBatch   bool                          `json:"frameBatch,omitempty"`
	State          map[string]ProtocolStateSlot  `json:"state,omitempty"`
	Indexes        map[string]ProtocolStateIndex `json:"indexes,omitempty"`
	Finalize       *Expression                   `json:"finalize,omitempty"`
	Observe        *Expression                   `json:"observe,omitempty"`
	Program        Expression                    `json:"program"`
	Effects        []ProtocolSemanticEffect      `json:"effects"`
	Capabilities   CapabilitySet                 `json:"capabilities,omitzero"`
}

// ProtocolDeliveryRange associates final wire text with references in one
// declared coordinate system. Selectors are read-only and return record arrays.
type ProtocolDeliveryRange struct {
	Name    string     `json:"name"`
	Measure Expression `json:"measure"`
	Texts   Expression `json:"texts"`
	Ranges  Expression `json:"ranges"`
}

// ProtocolStateSlot retains one bounded value between events. All updates read
// the preceding event's state; an omitted result clears that slot. Initial is
// optional, so a declaration can distinguish an absent identity from JSON null.
type ProtocolStateSlot struct {
	Schema  ValueSchema `json:"schema"`
	Initial Value       `json:"initial,omitzero"`
	Update  Expression  `json:"update"`
}

// ProtocolWireStateSlot declares retained wire-validation state. state_update
// actions supply its updates; unlisted slots retain their preceding values.
// Retention is bounded by total bytes and declared slot count. Each updated
// value separately satisfies its schema and node/depth limits.
type ProtocolWireStateSlot struct {
	Schema  ValueSchema `json:"schema"`
	Initial Value       `json:"initial,omitzero"`
}

// ProtocolStateIndex declares one request-owned identity table with an explicit
// key equality mode. Values are ordinary JSON unless CanRetainSource preserves
// an existing captured provenance receipt. Native envelopes remain untrusted.
type ProtocolStateIndex struct {
	KeyMode         string      `json:"keyMode"`
	Policy          string      `json:"policy"`
	Schema          ValueSchema `json:"schema"`
	CanRetainSource bool        `json:"retainSource,omitempty"`
}

// ProtocolSemanticEffect declares how a program handles a core semantic location.
// Path always addresses core data and Target addresses wire data. When observes
// the direction's input, one CacheIntent for cache_intents scope, or the
// selected Value leaf for request_field scope. Default declares an equivalent
// target value; it does not authorize deletion of a different selected value.
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

// RejectDiagnostic classifies a declared rejection. Mapping effects supply their
// own direction and business path. Wire assertions may override both fields;
// reject expressions may override direction but use their own explicit path.
type RejectDiagnostic struct {
	Code       IssueCode `json:"code"`
	Stage      string    `json:"stage"`
	Suggestion string    `json:"suggestion,omitempty"`
	Direction  Direction `json:"direction,omitempty"`
	Path       string    `json:"path,omitempty"`
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
	expressions := newExpressionCompiler(limits, mapping.Expressions)
	expressions.streamDispositions = make(map[string]bool)
	for _, rule := range mapping.ConversionDefaults {
		if rule.StreamDisposition != nil {
			expressions.streamDispositions[rule.ID] = true
		}
	}
	for _, direction := range sortedKeys(mapping.Directions) {
		if !slices.Contains(DirectionCatalog(), direction) {
			return nil, fmt.Errorf("/mapping/directions/%s: unsupported package direction", direction)
		}
		entry := mapping.Directions[direction]
		wire, exists := wireSpec.resolvedDocuments[entry.Document]
		if !exists {
			return nil, fmt.Errorf("/mapping/directions/%s/document: requires a declared wire document", direction)
		}
		contract := wireSpec.documents[entry.Document]
		if contract.packageStream != nil && !isEventDirection(direction) {
			return nil, fmt.Errorf("/mapping/directions/%s/document: stream documents require an event direction", direction)
		}
		compiled.wireContracts[direction] = contract
		if err := compiled.bindFinalDocument(direction, entry, wireSpec.documents); err != nil {
			return nil, err
		}
		for _, capability := range sortedKeys(entry.Capabilities) {
			if !mapping.Capabilities[capability] {
				return nil, fmt.Errorf("/mapping/directions/%s/capabilities: undeclared capability %q", direction, capability)
			}
		}
		var outputNamespaces map[string]int
		if stream := compiled.selectFinalWireContract(direction).packageStream; stream != nil {
			outputNamespaces = stream.namespaceSlots
		}
		finalWire := wire
		if entry.FinalDocument != "" {
			finalWire = wireSpec.resolvedDocuments[entry.FinalDocument]
		}
		program, effects, err := compileProtocolProgram("/mapping/directions/"+string(direction), entry, direction, wire, finalWire, wireSpec.documentHashes, outputNamespaces, expressions, mapping.CoreVersion, mapping.DSLVersion)
		if err != nil {
			return nil, err
		}
		program.capabilities = mapping.Capabilities
		if entry.Capabilities != nil {
			program.capabilities = entry.Capabilities
		}
		compiled.mappings[direction], compiled.packagePlan.effects[direction] = program, effects
	}
	if err := compiled.bindPackageOperations(spec, mapping); err != nil {
		return nil, err
	}
	if err := compiled.compileContinuation(mapping.Continuation, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageModelMappings(spec, mapping, wireSpec, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageTaskMappings(spec, mapping, wireSpec, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compilePackageAgent(mapping.Agent, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compileModelPolicy(mapping.ModelPolicy, expressions); err != nil {
		return nil, err
	}
	if err := compiled.bindRequestProjection(document); err != nil {
		return nil, locateMappingError("/mapping/directions/encode_request/effects", err)
	}
	if err := compiled.compileConversionDefaults(mapping, expressions); err != nil {
		return nil, err
	}
	if err := compiled.compileTokenCounting(document, wireSpec, expressions); err != nil {
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
	for name, count := range compiled.packagePlan.tokenCounts {
		count.request.hash, count.request.definition = compiled.hash, compiled.definition
		identity, err := EncodeValue(struct{ Revision, Operation string }{compiled.hash, name})
		if err != nil {
			return nil, err
		}
		count.request.executionHash = hashValue(identity)
	}
	return compiled, nil
}

func validateProtocolSamples(mapping ProtocolMapping, operations map[string]Operation, limits Limits) error {
	count := len(mapping.Samples) + len(mapping.ModelSamples) + len(mapping.TokenCountSamples) + len(mapping.TaskSamples) + len(mapping.SessionSamples)
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
