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
	semanticRanges         *deliveryRangeStream
	primitives             *packageStreamPrimitives
	scope                  Scope
	slots                  []Value
	indexes                []map[string]Value
	indexSources           []map[string]JSONOutput
	indexItems, indexBytes int
	items                  int
	bytes                  int
	slotBytes              int
	hasRendered            bool
	hasTerminal            bool
}

// Updates are staged separately from retained state. The outer event boundary
// commits only after decoding, provenance and native reconciliation succeed.
type packageStreamTransaction struct {
	semanticRanges                 *deliveryRangeStream
	conversion                     *streamConversionBinding
	dispositions                   []streamDispositionRequest
	diagnostics                    *DiagnosticSink
	pendingDiagnostics             DiagnosticSink
	trackChannels                  map[string]int
	bufferUpdates                  map[streamTrackKey]*pendingStreamBuffer
	bufferByteDelta                int
	primitiveItems                 int
	toolUpdates                    map[streamTrackKey]*pendingStreamTool
	toolCallUpdates                map[streamTrackKey]string
	toolBufferDelta                int
	textUpdates                    map[streamTrackKey]*pendingStreamText
	arrayUpdates                   map[streamTrackKey]streamArrayTracker
	positionUpdates                map[streamTrackKey]*pendingStreamPositions
	positionEnds                   map[string]int64
	positionItems                  int
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
	indexUpdates                   []packageIndexUpdate
	indexSourceUpdates             []map[string]JSONOutput
	indexItemDelta, indexByteDelta int
	bytes                          int
	slotBytes                      int
	hasRendered                    bool
	hasTerminal                    bool
}

func compilePackageState(declarations map[string]ProtocolStateSlot, indexes []compiledPackageIndex, direction Direction, path string, compiler *expressionCompiler, scope *expressionScope) (*compiledPackageState, error) {
	slots := make(map[string]ProtocolWireStateSlot, len(declarations))
	for name, declaration := range declarations {
		slots[name] = ProtocolWireStateSlot{Schema: declaration.Schema, Initial: declaration.Initial}
	}
	plan, err := compilePackageStateSlots(slots, indexes, direction, path, compiler, scope)
	if err != nil || plan == nil {
		return plan, err
	}
	for index, name := range sortedKeys(declarations) {
		update, err := compiler.compilePlan(declarations[name].Update, plan.slots[index].path+"/update", *scope, 1)
		if err != nil {
			return nil, err
		}
		plan.slots[index].update = update
	}
	return plan, nil
}

func compilePackageStateSlots(declarations map[string]ProtocolWireStateSlot, indexes []compiledPackageIndex, direction Direction, path string, compiler *expressionCompiler, scope *expressionScope) (*compiledPackageState, error) {
	if len(declarations)+len(indexes) == 0 {
		return nil, nil
	}
	if !isEventDirection(direction) {
		return nil, fmt.Errorf("%s: state requires an event direction", path)
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
		plan.initialBytes += len(plan.initial[index].raw)
		if plan.initialBytes > compiler.limits.BufferBytes {
			return nil, locateMappingError(path, resourceFailure("stream", "state_bytes", compiler.limits.BufferBytes, plan.initialBytes))
		}
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
	transaction := &packageStreamTransaction{owner: options.State, key: key, plan: plan, scope: options.Scope, previous: previous, conversion: options.conversion, diagnostics: options.Diagnostics}
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
	next, bytes, err := evaluatePackageStateSlots(transaction.plan, previous, state)
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

// Slot evaluation shares the preceding snapshot and returns owned staged values.
// Each consumer accounts for those values with its own identities and buffers.
func evaluatePackageStateSlots(plan *compiledPackageState, previous []Value, state evaluation) ([]Value, int, error) {
	state.streamSlots = previous
	next := make([]Value, len(plan.slots))
	bytes := 0
	for index, slot := range plan.slots {
		value, err := evaluatePackageStateSlot(slot, slot.update, previous[index], state)
		if err != nil {
			return nil, 0, err
		}
		next[index] = value
		bytes += len(value.raw)
	}
	return next, bytes, nil
}

func evaluatePackageStateSlot(slot compiledPackageStateSlot, update *compiledOutputPlan, previous Value, state evaluation) (Value, error) {
	output, err := update.build(state)
	if err != nil || output.output.IsZero() {
		return Value{}, err
	}
	value, err := output.output.Encode(state.ctx)
	if err != nil {
		return Value{}, err
	}
	if spanIdentity(value.raw) == spanIdentity(previous.raw) {
		return value, nil
	}
	if err := slot.schema.validate(state.ctx, value, slot.path, state.budget.limits); err != nil {
		return Value{}, err
	}
	// Selected fragments must not retain an entire frame.
	value.raw = strings.Clone(value.raw)
	return value, nil
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

// Index records, dynamic identities, trackers and interval spans share the
// owner's budget. Fixed slots and aliases of an existing identity add no item.
func (transaction *packageStreamTransaction) retainedItems() int {
	items := transaction.indexItemDelta + transaction.primitiveItems + transaction.positionItems
	primitives, ranges := transaction.primitiveBase, transaction.semanticRanges
	if transaction.previous != nil {
		items += transaction.previous.indexItems
		if primitives == nil {
			primitives = transaction.previous.primitives
		}
		if ranges == nil {
			ranges = transaction.previous.semanticRanges
		}
	}
	if primitives != nil {
		items += primitives.items + primitives.positionItems
	}
	if ranges != nil {
		items += ranges.items
	}
	return items
}

func (transaction *packageStreamTransaction) checkRetainedItems(additional int) error {
	items := transaction.owner.packageStateItems + transaction.retainedItems() + additional
	if transaction.previous != nil {
		items -= transaction.previous.items
	}
	limit := transaction.key.compiled.limits.StateItems
	if items > limit {
		return resourceFailure("stream", "state_items", limit, items)
	}
	return nil
}

// All retained payloads share one budget. Category deltas keep appending a
// frame constant-time without granting each table a separate buffer allowance.
func (transaction *packageStreamTransaction) retainedBytes() int {
	bytes := transaction.currentSlotBytes() + transaction.indexByteDelta + transaction.aliasBytes + transaction.trackedBytes + transaction.toolCallBytes + transaction.toolBufferDelta + transaction.bufferByteDelta
	ranges := transaction.semanticRanges
	if ranges == nil && transaction.previous != nil {
		ranges = transaction.previous.semanticRanges
	}
	if ranges != nil {
		bytes += ranges.bytes + ranges.stagedBytes
	}
	if transaction.previous != nil {
		bytes += transaction.previous.indexBytes
		if primitives := transaction.previous.primitives; primitives != nil {
			bytes += primitives.aliasBytes + primitives.trackedBytes + primitives.toolCallBytes + primitives.toolBuffered + primitives.buffered
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
		transaction.owner.packageStateItems -= transaction.previous.items
	}
	items := transaction.retainedItems()
	indexes, indexItems, indexBytes := transaction.commitIndexes()
	indexSources := transaction.commitIndexSources()
	transaction.owner.packageStateItems += items
	primitives := transaction.commitPrimitives()
	ranges := transaction.semanticRanges
	if ranges != nil {
		transaction.bytes -= ranges.stagedBytes
		if err := ranges.commit(); err != nil {
			panic(err) // Only a compiled SHA-256 tracker is cloned at commit.
		}
	} else if transaction.previous != nil {
		ranges = transaction.previous.semanticRanges
	}
	transaction.owner.packageStates[transaction.key] = &packageStreamState{
		semanticRanges: ranges,
		primitives:     primitives, scope: transaction.scope,
		slots: transaction.next, slotBytes: transaction.slotBytes,
		indexes: indexes, indexSources: indexSources, indexItems: indexItems, indexBytes: indexBytes, items: items,
		bytes: transaction.bytes, hasRendered: transaction.hasRendered, hasTerminal: transaction.hasTerminal,
	}
	transaction.owner.packageStateBytes += transaction.bytes
	transaction.commitDiagnostics()
	transaction.next = nil
}

func (state *EvaluationState) releasePackageState(key moduleStateKey) {
	if previous := state.packageStates[key]; previous != nil {
		state.packageStateItems -= previous.items
		state.packageStateBytes -= previous.bytes
		delete(state.packageStates, key)
	}
}
