package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type compiledPackageState struct {
	slots        []compiledPackageStateSlot
	initial      []Value
	indexes      []compiledPackageIndex
	initialBytes int
}

type compiledPackageStateSlot struct {
	path   string
	schema *wireSchemaPlan
	update *compiledOutputPlan
}

type packageStreamState struct {
	primitives             *packageStreamPrimitives
	scope                  Scope
	slots                  []Value
	indexes                []map[string]Value
	indexSources           []map[string]JSONOutput
	indexItems, indexBytes int
	bytes                  int
	slotBytes              int
	hasRendered            bool
	hasTerminal            bool
}

// Updates are staged separately from retained state. The outer event boundary
// commits only after decoding, provenance and native reconciliation succeed.
type packageStreamTransaction struct {
	trackChannels                  map[string]uint8
	trackItems                     int
	toolUpdates                    map[streamTrackKey]*pendingStreamTool
	toolCallUpdates                map[streamTrackKey]string
	toolBufferDelta                int
	textUpdates                    map[streamTrackKey]*pendingStreamText
	primitiveBase                  *packageStreamPrimitives
	aliasUpdates                   map[string]string
	aliasBytes                     int
	trackedBytes                   int
	toolCallBytes                  int
	owner                          *EvaluationState
	key                            moduleStateKey
	plan                           *compiledPackageState
	scope                          Scope
	previous                       *packageStreamState
	next                           []Value
	indexUpdates                   []map[string]Value
	indexSourceUpdates             []map[string]JSONOutput
	indexItemDelta, indexByteDelta int
	bytes                          int
	slotBytes                      int
	hasRendered                    bool
	hasTerminal                    bool
}

func compilePackageState(declarations map[string]ProtocolStateSlot, indexes []compiledPackageIndex, direction Direction, path string, compiler *expressionCompiler, scope *expressionScope) (*compiledPackageState, error) {
	if len(declarations)+len(indexes) == 0 {
		return nil, nil
	}
	if !isEventDirection(direction) || len(declarations)+len(indexes) > compiler.limits.StateItems {
		return nil, fmt.Errorf("%s: state requires an event direction and bounded slot count", path)
	}
	names := sortedKeys(declarations)
	plan := &compiledPackageState{slots: make([]compiledPackageStateSlot, len(names)), initial: make([]Value, len(names)), indexes: indexes}
	scope.stateSlots = make(map[string]expressionBinding, len(names))
	for index, name := range names {
		if !definitionIdentifier.MatchString(name) {
			return nil, fmt.Errorf("%s/%s: invalid state slot identifier", path, escapePointer(name))
		}
		declaration := declarations[name]
		schema := declaration.Schema
		scope.stateSlots[name] = expressionBinding{slot: index, result: schema.Type, schema: &schema}
		plan.slots[index].path = path + "/" + escapePointer(name)
		var err error
		plan.slots[index].schema, err = compileWireSchemaPlan(&schema, compiler.limits)
		if err != nil {
			return nil, locateMappingError(plan.slots[index].path+"/schema", err)
		}
		if !declaration.Initial.IsZero() {
			if err := plan.slots[index].schema.validate(context.Background(), declaration.Initial, plan.slots[index].path+"/initial", compiler.limits); err != nil {
				return nil, err
			}
			plan.initial[index] = Value{raw: strings.Clone(declaration.Initial.raw)}
		}
	}
	var err error
	plan.initialBytes, err = measurePackageState(plan.initial, compiler.limits)
	if err != nil {
		return nil, locateMappingError(path, err)
	}
	for index, name := range names {
		declaration := declarations[name]
		update, err := compiler.compilePlan(declaration.Update, plan.slots[index].path+"/update", *scope, 1)
		if err != nil {
			return nil, err
		}
		plan.slots[index].update = update
	}
	return plan, nil
}

func (compiled *Compiled) beginPackageTransaction(direction Direction, options EvaluationContext) (EvaluationContext, *packageStreamTransaction, error) {
	if compiled.packagePlan == nil {
		options.packageTransaction = nil
		return options, nil, nil
	}
	plan := compiled.mappings[direction].streamState
	if plan == nil {
		options.packageTransaction = nil
		return options, nil, nil
	}
	key := moduleStateKey{compiled: compiled, direction: direction}
	if active := options.packageTransaction; active != nil && active.key == key {
		if active.scope != options.Scope {
			return options, nil, streamIssue(ResourceScopeMismatch, "/state", "event state belongs to another stream scope")
		}
		return options, nil, nil
	}
	if options.State == nil {
		return options, nil, fmt.Errorf("declared event state requires one EvaluationState per stream")
	}
	previous := options.State.packageStates[key]
	if previous != nil && previous.scope != options.Scope {
		return options, nil, streamIssue(ResourceScopeMismatch, "/state", "event state belongs to another stream scope")
	}
	if previous == nil && len(options.State.packageStates)+len(options.State.modules)+len(options.State.initialized) >= compiled.limits.StateItems {
		return options, nil, resourceFailure("stream", "state_instances", compiled.limits.StateItems, len(options.State.packageStates)+len(options.State.modules)+len(options.State.initialized)+1)
	}
	transaction := &packageStreamTransaction{owner: options.State, key: key, plan: plan, scope: options.Scope, previous: previous}
	if previous != nil {
		transaction.hasRendered, transaction.hasTerminal = previous.hasRendered, previous.hasTerminal
	}
	options.packageTransaction = transaction
	return options, transaction, nil
}

func (transaction *packageStreamTransaction) values() []Value {
	if transaction == nil {
		return nil
	}
	if transaction.next != nil {
		return transaction.next
	}
	if transaction.previous != nil {
		return transaction.previous.slots
	}
	return transaction.plan.initial
}

func (transaction *packageStreamTransaction) stage(state evaluation, isRendered bool) error {
	if transaction == nil {
		return nil
	}
	previous := transaction.values()
	state.streamSlots = previous
	next := make([]Value, len(transaction.plan.slots))
	for index, slot := range transaction.plan.slots {
		output, err := slot.update.build(state)
		if err != nil {
			return err
		}
		if output.output.IsZero() {
			continue
		}
		value, err := output.output.Encode(state.ctx)
		if err != nil {
			return err
		}
		if err := slot.schema.validate(state.ctx, value, slot.path, state.budget.limits); err != nil {
			return err
		}
		// Even a tiny selected string can alias the entire original frame.
		// Already-owned slots can be retained by storage identity without a scan.
		if spanIdentity(value.raw) != spanIdentity(previous[index].raw) {
			value.raw = strings.Clone(value.raw)
		}
		next[index] = value
	}
	bytes, err := measurePackageState(next, state.budget.limits)
	if err != nil {
		return err
	}
	if err := transaction.checkRetainedBytes(bytes - transaction.currentSlotBytes()); err != nil {
		return err
	}
	if err := state.ctx.Err(); err != nil {
		return err
	}
	if !isPackageDecodeDirection(transaction.key.direction) {
		kind, err := state.readPackageInput([]string{"type"})
		if err != nil {
			return err
		}
		transaction.hasRendered = transaction.hasRendered || isRendered
		transaction.hasTerminal = transaction.hasTerminal || kind.value.IsString(string(ResponseFinished)) || kind.value.IsString(string(OperationFailed)) || kind.value.IsString(string(OperationCancelled))
	}
	transaction.next, transaction.slotBytes = next, bytes
	transaction.bytes = transaction.retainedBytes()
	return nil
}

func (transaction *packageStreamTransaction) currentSlotBytes() int {
	if transaction.next != nil {
		return transaction.slotBytes
	}
	if transaction.previous != nil {
		return transaction.previous.slotBytes
	}
	return transaction.plan.initialBytes
}

// All retained payloads share one budget. Category deltas keep appending a
// frame constant-time without granting each table a separate buffer allowance.
func (transaction *packageStreamTransaction) retainedBytes() int {
	bytes := transaction.currentSlotBytes() + transaction.indexByteDelta + transaction.aliasBytes + transaction.trackedBytes + transaction.toolCallBytes + transaction.toolBufferDelta
	if transaction.previous != nil {
		bytes += transaction.previous.indexBytes
		if primitives := transaction.previous.primitives; primitives != nil {
			bytes += primitives.aliasBytes + primitives.trackedBytes + primitives.toolCallBytes + primitives.toolBuffered
		}
	}
	return bytes
}

func (transaction *packageStreamTransaction) checkRetainedBytes(additional int) error {
	bytes := transaction.owner.packageStateBytes + transaction.retainedBytes() + additional
	if transaction.previous != nil {
		bytes -= transaction.previous.bytes
	}
	limit := transaction.key.compiled.limits.BufferBytes
	if bytes > limit {
		return resourceFailure("stream", "state_bytes", limit, bytes)
	}
	return nil
}

func measurePackageState(slots []Value, limits Limits) (int, error) {
	bytes := 0
	meter := valueTokenMeter{limits: limits, domain: "stream_state"}
	for _, value := range slots {
		bytes += len(value.raw)
		if bytes > limits.BufferBytes {
			return 0, resourceFailure("stream", "state_bytes", limits.BufferBytes, bytes)
		}
		if !value.IsZero() {
			if err := meter.scan(value.raw); err != nil {
				return 0, err
			}
		}
	}
	return bytes, nil
}

func (transaction *packageStreamTransaction) finish(err error) {
	if transaction == nil {
		return
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			transaction.owner.releasePackageState(transaction.key)
		}
		transaction.next = nil
		return
	}
	if transaction.next == nil {
		return
	}
	if transaction.owner.packageStates == nil {
		transaction.owner.packageStates = make(map[moduleStateKey]*packageStreamState)
	}
	if transaction.previous != nil {
		transaction.owner.packageStateBytes -= transaction.previous.bytes
	}
	indexes, items, indexBytes := transaction.commitIndexes()
	indexSources := transaction.commitIndexSources()
	transaction.owner.packageStateItems += transaction.indexItemDelta
	primitives := transaction.commitPrimitives()
	transaction.owner.packageStates[transaction.key] = &packageStreamState{
		primitives: primitives, scope: transaction.scope,
		slots: transaction.next, slotBytes: transaction.slotBytes,
		indexes: indexes, indexSources: indexSources, indexItems: items, indexBytes: indexBytes,
		bytes: transaction.bytes, hasRendered: transaction.hasRendered, hasTerminal: transaction.hasTerminal,
	}
	transaction.owner.packageStateBytes += transaction.bytes
	transaction.next = nil
}

func (state *EvaluationState) releasePackageState(key moduleStateKey) {
	if previous := state.packageStates[key]; previous != nil {
		state.packageStateItems -= previous.indexItems
		state.packageStateBytes -= previous.bytes
		delete(state.packageStates, key)
	}
}
