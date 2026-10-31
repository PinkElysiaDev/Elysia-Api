package protocol

import "fmt"

type streamField struct {
	path     string
	pointer  []string
	value    *compiledOutputPlan
	fromRoot bool
}

type wireFrameClass uint8

const (
	wireFrameOrdinary wireFrameClass = iota
	wireFrameGenerated
	wireFrameAuxiliary
	wireFrameControl
	wireFrameState
)

type wireStreamAction uint8

const (
	wireObserve wireStreamAction = iota
	wireStart
	wireEntityStart
	wireEntityAppend
	wireEntitySnapshot
	wireEntityVerify
	wireEntityFinish
	wireFinish
	wireFail
	wireCancel
)

type compiledWireNamespace struct {
	name    string
	keyType JSONType
}
type compiledWireEntity struct {
	name                     string
	namespace                int
	payload                  string
	retainCompletedJSON      bool
	isArray, closeWithParent bool
	childNamespaces          []int
	arrayItems               *wireSchemaPlan
	schema                   *wireSchemaPlan
	parentNamespace          int
	parentEntities           []int
	identities               []compiledWireEntityIdentity
}
type compiledWireEntityIdentity struct {
	name                                          string
	kind                                          JSONType
	requiredAtStart, isRaw, isUnique, allowsEmpty bool
}
type compiledEntityIdentitySelection struct {
	slot  int
	field streamField
}

type compiledPackageStreamContract struct {
	discriminator, responseID, sequence                            streamField
	events                                                         map[string][]compiledPackageStreamEvent
	canInheritResponseID, isResponseIDOptional, isSequenceOptional bool
	sequenceDiagnostic                                             *RejectDiagnostic
	sequenceReason                                                 string
	finishOpenEntities                                             bool
	identities                                                     []compiledStreamIdentity
	namespaces                                                     []compiledWireNamespace
	namespaceSlots                                                 map[string]int
	entities                                                       []compiledWireEntity
	entitySlots                                                    map[string]int
	bindingSlots                                                   int
	state                                                          *compiledPackageState
}
type compiledStreamIdentity struct {
	field           streamField
	kind            JSONType
	when, key       *compiledOutputPlan
	keyPath         string
	isRaw, isUnique bool
	diagnostic      *RejectDiagnostic
}
type compiledPackageStreamEvent struct {
	action                                                                              wireStreamAction
	frameClass                                                                          wireFrameClass
	entity, namespace, childrenNamespace                                                int
	when                                                                                *compiledOutputPlan
	foreach                                                                             streamField
	actions                                                                             []compiledPackageStreamEvent
	responseID, key, parent, ordinal, offset, properties, childrenCount, delta, failure streamField
	identities                                                                          []compiledEntityIdentitySelection
	isControl, isImplicit, canPrecedeResponse, canFollowTerminal, isResponseIndependent bool
	isArrayItem                                                                         bool
	isJSONValue                                                                         bool
	isPlaceholder                                                                       bool
	itemBinding, indexBinding                                                           int
	canBindItem, canBindIndex                                                           bool
	sealsPayload, verifiesCompleted                                                     bool
	verifiedChildren                                                                    []int
	requiredEntities                                                                    []int
	diagnostic                                                                          *RejectDiagnostic
	assertions                                                                          []compiledWireAssertion
	updates                                                                             []compiledWireStateUpdate
	isAssertion, isStateUpdate                                                          bool
}
type packageStreamCompiler struct {
	contract      *compiledPackageStreamContract
	expressions   *expressionCompiler
	limits        Limits
	actions       map[wireStreamAction]bool
	entityActions map[int]map[wireStreamAction]bool
	actionCount   int
	scope         expressionScope
}

func (expressions *expressionCompiler) compilePackageStreamContract(spec *ProtocolStreamSpec, path string) (*compiledPackageStreamContract, error) {
	if spec == nil {
		return nil, nil
	}
	contract := &compiledPackageStreamContract{events: make(map[string][]compiledPackageStreamEvent, len(spec.Events)),
		canInheritResponseID: spec.InheritResponseID, isResponseIDOptional: spec.OptionalResponseID,
		isSequenceOptional: spec.OptionalSequence, finishOpenEntities: spec.Lifecycle.OpenEntities == "finish",
		namespaceSlots: map[string]int{}, entitySlots: map[string]int{}}
	if spec.Lifecycle.EOF != "require_terminal" || spec.Lifecycle.OpenEntities != "reject" && spec.Lifecycle.OpenEntities != "finish" {
		return nil, fmt.Errorf("%s/lifecycle: requires EOF require_terminal and openEntities reject or finish", path)
	}
	if spec.OptionalSequence && spec.Sequence == "" {
		return nil, fmt.Errorf("%s/optionalSequence: requires a sequence selector", path)
	}
	if spec.SequenceReason != "" && spec.SequenceDiagnostic == nil {
		return nil, fmt.Errorf("%s/sequenceReason: requires a sequence diagnostic", path)
	}
	if spec.SequenceDiagnostic != nil {
		if spec.Sequence == "" {
			return nil, fmt.Errorf("%s/sequenceDiagnostic: requires a sequence selector", path)
		}
		if err := validateBoundaryDiagnostic(*spec.SequenceDiagnostic, path+"/sequenceDiagnostic"); err != nil {
			return nil, err
		}
		copy := *spec.SequenceDiagnostic
		contract.sequenceDiagnostic, contract.sequenceReason = &copy, spec.SequenceReason
	}
	for _, selector := range []struct {
		name, pointer string
		field         *streamField
	}{
		{"discriminator", spec.Discriminator, &contract.discriminator}, {"responseId", spec.ResponseID, &contract.responseID}, {"sequence", spec.Sequence, &contract.sequence}} {
		if selector.name == "sequence" && selector.pointer == "" {
			continue
		}
		field, err := compileStreamField(selector.pointer, path+"/"+selector.name, expressions.limits)
		if err != nil {
			return nil, err
		}
		*selector.field = field
	}
	if len(spec.Events) == 0 || len(spec.Events) > expressions.limits.Nodes {
		return nil, fmt.Errorf("%s/events: requires a nonempty bounded event table", path)
	}
	compiler := packageStreamCompiler{contract: contract, expressions: expressions, limits: expressions.limits, actions: map[wireStreamAction]bool{}, entityActions: map[int]map[wireStreamAction]bool{}, scope: expressionScope{isWireContract: true}}
	var err error
	contract.state, err = compilePackageStateSlots(spec.State, nil, DecodeEvent, path+"/state", expressions, &compiler.scope)
	if err != nil {
		return nil, err
	}
	if err := compiler.compileEntities(spec, path); err != nil {
		return nil, err
	}
	contract.responseID.fromRoot = true
	for _, entry := range []struct {
		name  string
		value *Expression
		field *streamField
	}{
		{"discriminatorValue", spec.DiscriminatorValue, &contract.discriminator}, {"responseIdValue", spec.ResponseIDValue, &contract.responseID}} {
		if err := compiler.compileValue(entry.value, path+"/"+entry.name, entry.field); err != nil {
			return nil, err
		}
	}
	if err := compiler.compileIdentities(spec.Identities, path+"/identities"); err != nil {
		return nil, err
	}
	for _, tag := range sortedKeys(spec.Events) {
		at := path + "/events/" + escapePointer(tag)
		if tag == "" {
			return nil, fmt.Errorf("%s: empty event tag", at)
		}
		contract.events[tag], err = compiler.compileActions(spec.Events[tag], at, 1)
		if err != nil {
			return nil, err
		}
	}
	if err := compiler.checkLifecycle(path); err != nil {
		return nil, err
	}
	return contract, nil
}

func (compiler *packageStreamCompiler) compileValue(value *Expression, path string, field *streamField) error {
	if value == nil {
		return nil
	}
	program, err := compiler.expressions.compilePlan(*value, path, compiler.scope, 1)
	if err == nil {
		field.value = program
		compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, program.bindingSlots)
	}
	return err
}

func (compiler *packageStreamCompiler) compileIdentities(identities []ProtocolStreamIdentitySpec, path string) error {
	if len(identities) > compiler.limits.Nodes {
		return fmt.Errorf("%s: identity declarations exceed the configuration node limit", path)
	}
	seen := make(map[string]bool, len(identities))
	for index, identity := range identities {
		at := fmt.Sprintf("%s/%d", path, index)
		if seen[identity.Path] || identity.Type != StringType && identity.Type != IntegerType {
			return fmt.Errorf("%s: identity fields require distinct paths and string or integer types", at)
		}
		field, err := compileStreamField(identity.Path, at+"/path", compiler.limits)
		if err != nil {
			return err
		}
		condition, err := compiler.compilePredicate(identity.When, at+"/when")
		if err != nil {
			return err
		}
		field.fromRoot = true
		entry := compiledStreamIdentity{field: field, kind: identity.Type, when: condition, isUnique: identity.Unique}
		if err := compiler.compileIdentitySelection(identity, at, &entry); err != nil {
			return err
		}
		compiler.contract.identities = append(compiler.contract.identities, entry)
		seen[identity.Path] = true
	}
	return nil
}

func (compiler *packageStreamCompiler) compileIdentitySelection(spec ProtocolStreamIdentitySpec, path string, identity *compiledStreamIdentity) error {
	if spec.Comparison != "" && spec.Comparison != "typed" && spec.Comparison != "raw_json" {
		return fmt.Errorf("%s/comparison: requires raw_json or the default typed equality", path)
	}
	identity.isRaw = spec.Comparison == "raw_json"
	if err := compiler.compileValue(spec.Value, path+"/value", &identity.field); err != nil {
		return err
	}
	if spec.Key == nil && (spec.Unique || spec.KeyPath != "") {
		return fmt.Errorf("%s: unique and keyPath require a key expression", path)
	}
	if spec.Key != nil {
		if _, err := compileStreamField(spec.KeyPath, path+"/keyPath", compiler.limits); err != nil {
			return err
		}
		var err error
		identity.key, err = compiler.expressions.compilePlan(*spec.Key, path+"/key", compiler.scope, 1)
		if err != nil {
			return err
		}
		compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, identity.key.bindingSlots)
		identity.keyPath = spec.KeyPath
	}
	if spec.Diagnostic != nil {
		if err := validateBoundaryDiagnostic(*spec.Diagnostic, path+"/diagnostic"); err != nil {
			return err
		}
		copy := *spec.Diagnostic
		identity.diagnostic = &copy
	}
	return nil
}

func (compiler *packageStreamCompiler) compileSelection(spec *ProtocolStreamValueSpec, path string) (streamField, error) {
	if spec == nil {
		return streamField{}, nil
	}
	field, err := compileStreamField(spec.Path, path+"/path", compiler.limits)
	if err != nil {
		return field, err
	}
	err = compiler.compileValue(spec.Value, path+"/value", &field)
	return field, err
}

func (compiler *packageStreamCompiler) compileActions(entries []ProtocolStreamActionSpec, path string, depth int) ([]compiledPackageStreamEvent, error) {
	compiler.actionCount += len(entries)
	if len(entries) == 0 || compiler.actionCount > compiler.limits.Nodes || depth > compiler.limits.Depth {
		return nil, fmt.Errorf("%s: requires nonempty bounded actions", path)
	}
	actions := make([]compiledPackageStreamEvent, 0, len(entries))
	for index, entry := range entries {
		at := fmt.Sprintf("%s/%d", path, index)
		event := compiledPackageStreamEvent{entity: -1, namespace: -1, childrenNamespace: -1}
		var err error
		if entry.ForEach != "" {
			if entry.Action != "" || hasStreamEntitySelectors(entry) || entry.ResponseID != "" || entry.Error != "" || entry.Implicit || entry.AllowBeforeStart || entry.AfterTerminal || entry.ResponseIndependent || entry.FrameClass != "" || len(entry.Assertions) != 0 || entry.Updates != nil {
				return nil, fmt.Errorf("%s: foreach only declares iteration and nested actions", at)
			}
			event.foreach, err = compileStreamField(entry.ForEach, at+"/foreach", compiler.limits)
			if err == nil {
				previous := compiler.scope
				err = compiler.bindIteration(entry, &event, at)
				if err == nil {
					event.actions, err = compiler.compileActions(entry.Actions, at+"/actions", depth+1)
				}
				compiler.scope = previous
			}
		} else {
			if len(entry.Actions) != 0 || entry.As != "" || entry.IndexAs != "" {
				return nil, fmt.Errorf("%s: nested actions require foreach", at)
			}
			if entry.Action == "assert" || entry.Action == "state_update" {
				event, err = compiler.compileCheckAction(entry, at)
			} else {
				if len(entry.Assertions) != 0 || entry.Updates != nil {
					return nil, fmt.Errorf("%s: assertions and updates require their own actions", at)
				}
				event, err = compiler.compileAction(entry, at)
			}
		}
		if err != nil {
			return nil, err
		}
		event.when, err = compiler.compilePredicate(entry.When, at+"/when")
		if err != nil {
			return nil, err
		}
		actions = append(actions, event)
	}
	return actions, nil
}

func (compiler *packageStreamCompiler) compileAction(spec ProtocolStreamActionSpec, path string) (compiledPackageStreamEvent, error) {
	event := compiledPackageStreamEvent{entity: -1, namespace: -1, childrenNamespace: -1, responseID: compiler.contract.responseID,
		isImplicit: spec.Implicit, canPrecedeResponse: spec.AllowBeforeStart, canFollowTerminal: spec.AfterTerminal}
	actions := map[string]wireStreamAction{"stream_start": wireStart, "entity_start": wireEntityStart, "entity_append": wireEntityAppend, "entity_snapshot": wireEntitySnapshot, "entity_verify": wireEntityVerify, "entity_finish": wireEntityFinish, "stream_finish": wireFinish, "stream_fail": wireFail, "stream_cancel": wireCancel, "observe": wireObserve, "control": wireObserve}
	var exists bool
	event.action, exists = actions[spec.Action]
	if !exists {
		return event, fmt.Errorf("%s/action: unknown external lifecycle action", path)
	}
	event.isControl = spec.Action == "control"
	event.isResponseIndependent = spec.ResponseIndependent
	if spec.ResponseIndependent && (spec.Action != "observe" || !spec.AllowBeforeStart || spec.FrameClass != "" && spec.FrameClass != "state") {
		return event, fmt.Errorf("%s/responseIndependent: requires an ordinary observation allowed before start", path)
	}

	if event.action >= wireEntityStart && event.action <= wireEntityFinish {
		event.frameClass = wireFrameGenerated
	}
	if spec.FrameClass != "" {
		if event.action != wireObserve {
			return event, fmt.Errorf("%s/frameClass: only observations declare a frame class", path)
		}
		switch spec.FrameClass {
		case "generated":
			event.frameClass = wireFrameGenerated
		case "auxiliary":
			event.frameClass = wireFrameAuxiliary
		case "control":
			event.frameClass = wireFrameControl
		case "state":
			if spec.Action != "observe" {
				return event, fmt.Errorf("%s/frameClass: state requires an observation", path)
			}
			event.frameClass = wireFrameState
		default:
			return event, fmt.Errorf("%s/frameClass: requires generated, auxiliary, state or control", path)
		}
	}

	if spec.Implicit && event.action != wireStart && event.action != wireEntityStart {
		return event, fmt.Errorf("%s/implicit: requires a start action", path)
	}
	if spec.AllowBeforeStart && event.action != wireObserve && event.action != wireFail && event.action != wireCancel {
		return event, fmt.Errorf("%s/allowBeforeStart: only observation or failure may precede start", path)
	}
	if spec.AfterTerminal && event.action != wireObserve {
		return event, fmt.Errorf("%s/afterTerminal: only observation may follow terminal", path)
	}
	if (event.action == wireFail) != (spec.Error != "") {
		return event, fmt.Errorf("%s/error: only stream_fail requires an error selector", path)
	}
	var err error
	if spec.ResponseID != "" {
		event.responseID, err = compileStreamField(spec.ResponseID, path+"/responseId", compiler.limits)
		if err != nil {
			return event, err
		}
	}
	if spec.Error != "" {
		event.failure, err = compileStreamField(spec.Error, path+"/error", compiler.limits)
		if err != nil {
			return event, err
		}
	}
	if err := compiler.compileEntitySelectors(spec, &event, path); err != nil {
		return event, err
	}
	compiler.actions[event.action] = true
	if event.entity >= 0 {
		if event.action == wireEntityVerify || event.verifiesCompleted {
			compiler.contract.entities[event.entity].retainCompletedJSON = true
		}
		if compiler.entityActions[event.entity] == nil {
			compiler.entityActions[event.entity] = map[wireStreamAction]bool{}
		}
		compiler.entityActions[event.entity][event.action] = true
	}
	return event, nil
}

func compileStreamField(pointer, path string, limits Limits) (streamField, error) {
	parts, err := parsePointer(pointer)
	if err != nil {
		return streamField{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(parts) == 0 || len(parts) > limits.Depth {
		return streamField{}, fmt.Errorf("%s: requires a non-root pointer within the depth limit", path)
	}
	return streamField{path: pointer, pointer: parts}, nil
}
