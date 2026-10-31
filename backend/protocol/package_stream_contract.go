package protocol

import (
	"fmt"
	"slices"
)

type streamField struct {
	path     string
	pointer  []string
	value    *compiledOutputPlan
	fromRoot bool
}

type compiledPackageStreamContract struct {
	discriminator, responseID, itemID, sequence streamField
	events                                      map[string][]compiledPackageStreamEvent
	hasTools                                    bool
	hasTypedItems                               bool
	canInheritResponseID                        bool
	isResponseIDOptional                        bool
	isItemIDIndex                               bool
	isSequenceOptional                          bool
	sequenceDiagnostic                          *RejectDiagnostic
	closeItemsOnFinish                          bool
	identities                                  []compiledStreamIdentity
	itemKinds                                   []NodeKind
	bindingSlots                                int
	state                                       *compiledPackageState
}

type compiledStreamIdentity struct {
	field      streamField
	kind       JSONType
	when       *compiledOutputPlan
	key        *compiledOutputPlan
	keyPath    string
	isRaw      bool
	isUnique   bool
	diagnostic *RejectDiagnostic
}

type compiledPackageStreamEvent struct {
	kind                                       NodeKind
	action                                     EventType
	when                                       *compiledOutputPlan
	foreach                                    streamField
	actions                                    []compiledPackageStreamEvent
	responseID, itemID                         streamField
	parentID, ordinal                          streamField
	wireID, role, status                       streamField
	properties, childrenCount                  streamField
	delta, failure                             streamField
	usage                                      [3]streamField
	tool                                       *compiledStreamTool
	isTool                                     bool
	isControl                                  bool
	isImplicit                                 bool
	isSparseIdentity                           bool
	canPrecedeResponse                         bool
	canOmitUsageCounters                       bool
	itemBinding, indexBinding                  int
	canBindItem, canBindIndex                  bool
	sealsText, verifiesItem, verifiesCompleted bool
	requiredItems                              []NodeKind
	diagnostic                                 *RejectDiagnostic
	assertions                                 []compiledWireAssertion
	isAssertion, isStateUpdate                 bool
}

type compiledStreamTool struct {
	name, callID streamField
}

func compilePackageStreamContract(spec *ProtocolStreamSpec, path string, limits Limits) (*compiledPackageStreamContract, error) {
	if spec == nil {
		return nil, nil
	}
	contract := &compiledPackageStreamContract{events: make(map[string][]compiledPackageStreamEvent, len(spec.Events)), canInheritResponseID: spec.InheritResponseID, isResponseIDOptional: spec.OptionalResponseID, isItemIDIndex: spec.IsItemIDIndex, isSequenceOptional: spec.OptionalSequence, closeItemsOnFinish: spec.CloseItemsOnFinish}
	if spec.OptionalSequence && spec.Sequence == "" {
		return nil, fmt.Errorf("%s/optionalSequence: requires a sequence selector", path)
	}
	if spec.SequenceDiagnostic != nil {
		if spec.Sequence == "" {
			return nil, fmt.Errorf("%s/sequenceDiagnostic: requires a sequence selector", path)
		}
		if err := validateBoundaryDiagnostic(*spec.SequenceDiagnostic, path+"/sequenceDiagnostic"); err != nil {
			return nil, err
		}
		copy := *spec.SequenceDiagnostic
		contract.sequenceDiagnostic = &copy
	}
	for _, selector := range []struct {
		name, path string
		field      *streamField
	}{{"discriminator", spec.Discriminator, &contract.discriminator}, {"responseId", spec.ResponseID, &contract.responseID}, {"itemId", spec.ItemID, &contract.itemID}, {"sequence", spec.Sequence, &contract.sequence}} {
		if selector.name == "sequence" && selector.path == "" {
			continue
		}
		field, err := compileStreamField(selector.path, path+"/"+selector.name, limits)
		if err != nil {
			return nil, err
		}
		*selector.field = field
	}
	if len(spec.Events) == 0 || len(spec.Events) > limits.StateItems {
		return nil, fmt.Errorf("%s/events: requires a nonempty bounded event table", path)
	}
	compiler := packageStreamCompiler{contract: contract, expressions: &expressionCompiler{limits: limits}, limits: limits,
		actions: map[EventType]bool{}, toolActions: map[EventType]bool{}}
	var err error
	contract.state, err = compilePackageState(spec.State, nil, DecodeEvent, path+"/state", compiler.expressions, &compiler.scope)
	if err != nil {
		return nil, err
	}
	if contract.state != nil {
		for _, slot := range contract.state.slots {
			contract.bindingSlots = max(contract.bindingSlots, slot.update.bindingSlots)
		}
	}
	if spec.ItemKinds != nil {
		if len(spec.ItemKinds) == 0 {
			return nil, fmt.Errorf("%s/itemKinds: explicit item kinds must not be empty", path)
		}
		for _, kind := range spec.ItemKinds {
			if !slices.Contains([]NodeKind{TextNode, ReasoningNode, RefusalNode, ToolCallNode, MessageNode}, kind) || slices.Contains(contract.itemKinds, kind) {
				return nil, fmt.Errorf("%s/itemKinds: requires distinct supported item kinds", path)
			}
			contract.itemKinds = append(contract.itemKinds, kind)
		}
		compiler.itemActions = make(map[NodeKind]map[EventType]bool, len(spec.ItemKinds))
	}
	contract.responseID.fromRoot = true
	for _, field := range []struct {
		name   string
		value  *Expression
		target *streamField
	}{{"discriminatorValue", spec.DiscriminatorValue, &contract.discriminator}, {"responseIdValue", spec.ResponseIDValue, &contract.responseID}, {"itemIdValue", spec.ItemIDValue, &contract.itemID}} {
		if err := compiler.compileValue(field.value, path+"/"+field.name, field.target); err != nil {
			return nil, err
		}
	}
	if err := compiler.compileIdentities(spec.Identities, path+"/identities"); err != nil {
		return nil, err
	}
	for _, tag := range sortedKeys(spec.Events) {
		eventPath := path + "/events/" + escapePointer(tag)
		if tag == "" {
			return nil, fmt.Errorf("%s: event tag must not be empty", eventPath)
		}
		var err error
		contract.events[tag], err = compiler.compileActions(spec.Events[tag], eventPath, 1)
		if err != nil {
			return nil, err
		}
	}
	if err := compiler.checkLifecycle(path); err != nil {
		return nil, err
	}
	return contract, nil
}

type packageStreamCompiler struct {
	contract             *compiledPackageStreamContract
	expressions          *expressionCompiler
	limits               Limits
	actions, toolActions map[EventType]bool
	itemActions          map[NodeKind]map[EventType]bool
	actionCount          int
	scope                expressionScope
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
	if len(identities) > compiler.limits.StateItems {
		return fmt.Errorf("%s: identity fields exceed the stream item limit", path)
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
	if spec.Comparison != "" && spec.Comparison != "raw_json" {
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

func (compiler *packageStreamCompiler) compileActions(entries []ProtocolStreamActionSpec, path string, depth int) ([]compiledPackageStreamEvent, error) {
	compiler.actionCount += len(entries)
	if len(entries) == 0 || compiler.actionCount > compiler.limits.StateItems || depth > compiler.limits.Depth {
		return nil, fmt.Errorf("%s: requires nonempty actions within the stream item and depth limits", path)
	}
	actions := make([]compiledPackageStreamEvent, 0, len(entries))
	for index, entry := range entries {
		at := fmt.Sprintf("%s/%d", path, index)
		var event compiledPackageStreamEvent
		var err error
		if entry.ForEach != "" {
			if entry.Action != "" || entry.Kind != "" || entry.ResponseID != "" || entry.ItemID != "" || entry.ItemIDValue != nil || entry.Text != "" || entry.Arguments != "" || entry.Tool != nil || entry.Error != "" || entry.Usage != nil || entry.Implicit || entry.SparseIdentity || entry.AllowBeforeStart || len(entry.Assertions) != 0 || hasStreamItemSelectors(entry) {
				return nil, fmt.Errorf("%s: foreach declares only its selector, condition and nested actions", at)
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
				return nil, fmt.Errorf("%s/actions: nested actions require foreach", at)
			}
			if entry.Action == "assert" || entry.Action == "state_update" {
				event, err = compiler.compileCheckAction(entry, at, depth)
			} else {
				if len(entry.Assertions) != 0 {
					return nil, fmt.Errorf("%s/assertions: assertions require an assert action", at)
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

func (compiler *packageStreamCompiler) compileAction(entry ProtocolStreamActionSpec, path string) (compiledPackageStreamEvent, error) {
	event, err := compileStreamEvent(entry, path, compiler.limits)
	if err != nil {
		return event, err
	}
	if entry.Implicit && event.action != ResponseStarted && event.action != ItemStarted || entry.SparseIdentity && (!event.isTool || event.action != ItemStarted) {
		return event, fmt.Errorf("%s: implicit requires a start action and sparseIdentity requires tool_start", path)
	}
	if entry.AllowBeforeStart && event.action != OperationFailed && event.action != OperationCancelled && event.action != UsageUpdated && entry.Action != "observe" {
		return event, fmt.Errorf("%s/allowBeforeStart: only failure, cancel, usage or observation may precede the response", path)
	}
	event.canPrecedeResponse = entry.AllowBeforeStart
	event.isImplicit, event.isSparseIdentity = entry.Implicit, entry.SparseIdentity
	event.responseID, event.itemID = compiler.contract.responseID, compiler.contract.itemID
	for _, selector := range []struct {
		name, pointer string
		field         *streamField
	}{{"responseId", entry.ResponseID, &event.responseID}, {"itemId", entry.ItemID, &event.itemID}} {
		if selector.pointer != "" {
			*selector.field, err = compileStreamField(selector.pointer, path+"/"+selector.name, compiler.limits)
			if err != nil {
				return event, err
			}
		}
	}
	if err := compiler.compileValue(entry.ItemIDValue, path+"/itemIdValue", &event.itemID); err != nil {
		return event, err
	}
	if err := compiler.compileItemSelectors(entry, &event, path); err != nil {
		return event, err
	}
	compiler.contract.hasTypedItems = compiler.contract.hasTypedItems || entry.Kind != ""
	if err := compiler.recordItemAction(event, path); err != nil {
		return event, err
	}
	if event.isTool {
		compiler.contract.hasTools, compiler.toolActions[event.action] = true, true
	} else {
		compiler.actions[event.action] = true
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

func compileStreamEvent(spec ProtocolStreamActionSpec, path string, limits Limits) (compiledPackageStreamEvent, error) {
	event := compiledPackageStreamEvent{}
	switch spec.Action {
	case "response_start":
		event.action = ResponseStarted
	case "item_start":
		event.action = ItemStarted
	case "text_delta":
		event.action = ItemDelta
	case "text_snapshot":
		event.action = ItemSnapshot
	case "item_verify":
		event.action, event.verifiesItem = ItemSnapshot, true
	case "item_finish":
		event.action = ItemFinished
	case "tool_start":
		event.action, event.isTool = ItemStarted, true
	case "tool_delta":
		event.action, event.isTool = ItemDelta, true
	case "tool_snapshot":
		event.action, event.isTool = ItemSnapshot, true
	case "tool_finish":
		event.action, event.isTool = ItemFinished, true
	case "usage":
		event.action = UsageUpdated
	case "response_finish":
		event.action = ResponseFinished
	case "failure":
		event.action = OperationFailed
	case "cancel":
		event.action = OperationCancelled
	case "observe":
		event.action = MetadataUpdated
	case "control":
		event.action, event.isControl = MetadataUpdated, true
	default:
		return event, fmt.Errorf("%s/action: unsupported lifecycle action", path)
	}
	if spec.Kind != "" && (!slices.Contains([]string{"item_start", "text_delta", "text_snapshot", "item_finish", "item_verify"}, spec.Action) || !slices.Contains([]NodeKind{TextNode, ReasoningNode, RefusalNode, MessageNode}, spec.Kind) || spec.Kind == MessageNode && (spec.Action == "text_delta" || spec.Action == "text_snapshot")) {
		return event, fmt.Errorf("%s/kind: kind requires a compatible item lifecycle action", path)
	}
	event.kind = spec.Kind
	if event.kind == "" {
		event.kind = TextNode
	}
	canReadText := slices.Contains([]string{"item_start", "text_delta", "text_snapshot", "item_finish", "item_verify"}, spec.Action) && event.kind != MessageNode
	if spec.Text != "" && !canReadText || (spec.Action == "text_delta" || spec.Action == "text_snapshot") && spec.Text == "" || (event.action == OperationFailed) != (spec.Error != "") || (event.action == UsageUpdated) != (spec.Usage != nil) {
		return event, fmt.Errorf("%s: text, error and usage selectors belong only to their respective actions and are required there", path)
	}
	if spec.Action == "tool_snapshot" && spec.Arguments == "" {
		return event, fmt.Errorf("%s/arguments: tool_snapshot requires a JSON snapshot selector", path)
	}
	if (spec.Action == "tool_delta" || spec.Action == "tool_snapshot") != (spec.Arguments != "") || (spec.Action == "tool_start") != (spec.Tool != nil) {
		return event, fmt.Errorf("%s: tool identity belongs to tool_start and arguments to tool_delta/tool_snapshot; both are required there", path)
	}
	var err error
	if spec.Text != "" {
		event.delta, err = compileStreamField(spec.Text, path+"/text", limits)
	}
	if spec.Arguments != "" {
		event.delta, err = compileStreamField(spec.Arguments, path+"/arguments", limits)
	}
	if spec.Error != "" {
		event.failure, err = compileStreamField(spec.Error, path+"/error", limits)
	}
	if err != nil {
		return event, err
	}
	if spec.Tool != nil {
		event.tool = &compiledStreamTool{}
		for _, field := range []struct {
			name, pointer string
			target        *streamField
		}{{"name", spec.Tool.Name, &event.tool.name}, {"callId", spec.Tool.CallID, &event.tool.callID}} {
			selected, err := compileStreamField(field.pointer, path+"/tool/"+field.name, limits)
			if err != nil {
				return event, err
			}
			*field.target = selected
		}
	}
	if spec.Usage != nil {
		event.canOmitUsageCounters = spec.Usage.AllowEmpty
		if spec.Usage.Input == "" && spec.Usage.Output == "" && spec.Usage.Total == "" && !spec.Usage.AllowEmpty {
			return event, fmt.Errorf("%s/usage: requires at least one counter selector", path)
		}
		for index, selector := range []struct{ name, pointer string }{{"input", spec.Usage.Input}, {"output", spec.Usage.Output}, {"total", spec.Usage.Total}} {
			if selector.pointer == "" {
				continue
			}
			event.usage[index], err = compileStreamField(selector.pointer, path+"/usage/"+selector.name, limits)
			if err != nil {
				return event, err
			}
		}
	}
	return event, nil
}
