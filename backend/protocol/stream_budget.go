package protocol

// StreamResource is request-owned protocol state that can share a stream budget.
// Only protocol holders can contribute accounting; mappings cannot create it.
type StreamResource interface {
	bindStreamBudget(*StreamBudget) error
}

// StreamBudget bounds retained state across one stream's execution and validation
// owners. Like those owners, it belongs to a single goroutine. Limits must already
// be validated; binding a holder never expands either side's limits.
type StreamBudget struct {
	limits  Limits
	items   int
	bytes   int
	failure error
}

// NewStreamBudget creates an empty aggregate for validated protocol limits.
func NewStreamBudget(limits Limits) *StreamBudget { return &StreamBudget{limits: limits} }

// Bind joins existing holders before processing input. Rebinding the same holder
// to this budget is harmless; a holder cannot belong to two live streams.
func (budget *StreamBudget) Bind(resources ...StreamResource) error {
	for _, resource := range resources {
		if err := resource.bindStreamBudget(budget); err != nil {
			return err
		}
	}
	return nil
}

// Check reports whether this stream can still publish output. Capacity failure
// closes the aggregate even if cleanup subsequently releases its retained state.
func (budget *StreamBudget) Check() error { return budget.check(budget.items, budget.bytes) }

func (budget *StreamBudget) check(items, bytes int) error {
	if budget.failure != nil {
		return budget.failure
	}
	if items > budget.limits.StateItems {
		budget.failure = resourceFailure("stream", "state_items", budget.limits.StateItems, items)
	} else if bytes > budget.limits.BufferBytes {
		budget.failure = resourceFailure("stream", "state_bytes", budget.limits.BufferBytes, bytes)
	}
	return budget.failure
}

// A reservation records the holder's last committed contribution. Prospective
// checks substitute that contribution; they never publish a partial transaction.
type streamReservation struct {
	budget       *StreamBudget
	items, bytes int
}

func (reservation *streamReservation) bind(budget *StreamBudget, items, bytes int) error {
	if reservation.budget != nil {
		if reservation.budget == budget {
			return budget.Check()
		}
		return operationMetadataError("/state", "retained state already belongs to another stream budget")
	}
	if err := budget.check(budget.items+items, budget.bytes+bytes); err != nil {
		return err
	}
	reservation.budget = budget
	reservation.commit(items, bytes)
	return nil
}

func (reservation *streamReservation) check(items, bytes int) error {
	if budget := reservation.budget; budget != nil {
		return budget.check(budget.items-reservation.items+items, budget.bytes-reservation.bytes+bytes)
	}
	return nil
}

func (reservation *streamReservation) commit(items, bytes int) {
	if budget := reservation.budget; budget != nil {
		budget.items += items - reservation.items
		budget.bytes += bytes - reservation.bytes
		reservation.items, reservation.bytes = items, bytes
	}
}

func (reservation *streamReservation) release() {
	reservation.commit(0, 0)
	reservation.budget = nil
}

func (state *EvaluationState) bindStreamBudget(budget *StreamBudget) error {
	if err := state.retention.bind(budget, state.packageStateItems, state.packageStateBytes); err != nil {
		return err
	}
	for _, validation := range state.sourceStreams {
		if err := validation.bindStreamBudget(budget); err != nil {
			return err
		}
	}
	return nil
}

func (validation *WireStreamValidation) bindStreamBudget(budget *StreamBudget) error {
	state := validation.packageStream
	if state == nil {
		return operationMetadataError("/state", "shared wire accounting requires a package stream")
	}
	budget.limits.StateItems = min(budget.limits.StateItems, state.limits.StateItems)
	budget.limits.BufferBytes = min(budget.limits.BufferBytes, state.limits.BufferBytes)
	return state.retention.bind(budget, state.retainedItems(), state.retainedBytes())
}

func (state *packageStreamValidation) retainedItems() int {
	return state.entityCount + state.identityItems + state.rangeItems
}

func (state *packageStreamValidation) retainedBytes() int {
	return state.identityBytes + state.snapshotBytes + state.retainedSlotBytes() + state.bufferedBytes + state.rangeBytes
}

func (state *packageStreamValidation) commitRetention() error {
	if state.retention.budget == nil {
		return nil
	}
	items, bytes := state.retainedItems(), state.retainedBytes()
	if err := state.retention.check(items, bytes); err != nil {
		return err
	}
	state.retention.commit(items, bytes)
	return nil
}

func (transaction *packageStreamTransaction) checkSharedRetention() error {
	if transaction.owner.retention.budget == nil {
		return nil
	}
	items := transaction.owner.packageStateItems + transaction.retainedItems()
	bytes := transaction.owner.packageStateBytes + transaction.bytes
	if transaction.previous != nil {
		items -= transaction.previous.items
		bytes -= transaction.previous.bytes
	}
	return transaction.owner.retention.check(items, bytes)
}
