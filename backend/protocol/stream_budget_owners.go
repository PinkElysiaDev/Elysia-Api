package protocol

import "slices"

func (replay *EventReplay) bindStreamBudget(budget *StreamBudget) error {
	if replay.isReleased {
		return operationMetadataError("/state", "released replay cannot join a stream budget")
	}
	if replay.retention.budget == budget {
		return budget.Check()
	}
	budget.constrain(replay.limits)
	if err := replay.retention.bind(budget, replay.retainedItems(), replay.retainedBytes()); err != nil {
		return err
	}
	budget.sequences = append(budget.sequences, replay.sequence)
	return budget.Check()
}

func (collector *ResponseCollector) bindStreamBudget(budget *StreamBudget) error {
	if err := collector.replay.bindStreamBudget(budget); err != nil {
		return err
	}
	return collector.retention.bind(budget, collector.ownedItems(), collector.bytes)
}

func (collector *ResponseCollector) ownedItems() int {
	return collector.snapshotItems + len(collector.response.Metadata) + len(collector.response.Attributes) + len(collector.unmapped) + collector.metadataItems + collector.usageItems
}

func (binding *ContinuationBinding) bindStreamBudget(budget *StreamBudget) error {
	if binding.deliveryAborted {
		return operationMetadataError("/state", "aborted continuation cannot join a stream budget")
	}
	budget.constrain(binding.compiled.limits)
	return binding.retention.bind(budget, len(binding.entries), binding.bytes+binding.tokenView.bytes)
}

func (state *ConversionEventState) bindStreamBudget(budget *StreamBudget) error {
	if state.isReleased {
		return operationMetadataError("/state", "released conversion cannot join a stream budget")
	}
	if state.retention.budget == budget {
		return budget.Check()
	}
	if state.conversion != nil {
		budget.constrain(state.conversion.limits)
	}
	if state.usage != nil {
		size, err := measureSemanticValue(state.usage, budget.limits)
		if err != nil {
			return err
		}
		state.usageBytes = size.bytes
	}
	if state.terminal != nil {
		size, err := measureSemanticValue(state.terminal, semanticLimits(budget.limits))
		if err != nil {
			return err
		}
		state.terminalBytes = size.bytes
	}
	return state.retention.bind(budget, state.retainedItems(), state.retainedBytes())
}

func (state *ConversionEventState) retainedItems() int {
	items := len(state.pending)
	if state.usage != nil {
		items += len(state.usage.Details)
	}
	return items
}

func (state *ConversionEventState) retainedBytes() int {
	return state.bytes + state.openBytes + state.terminalBytes + state.usageBytes + state.id.Len() + state.model.Len() + state.route.Delivery.Created.Len()
}

func (state *ConversionEventState) commitRetention() error {
	items, bytes := state.retainedItems(), state.retainedBytes()
	if err := state.retention.check(items, bytes); err != nil {
		return err
	}
	state.retention.commit(items, bytes)
	return nil
}

func (state *ConversionEventState) setOpen(key string, isOpen bool) {
	if state.open[key] == isOpen {
		return
	}
	if isOpen {
		state.open[key] = true
		state.openBytes += len(key)
	} else {
		delete(state.open, key)
		state.openBytes -= len(key)
	}
}

func (replay *EventReplay) releaseRetention() {
	if budget := replay.retention.budget; budget != nil {
		index := slices.Index(budget.sequences, replay.sequence)
		budget.sequences = slices.Delete(budget.sequences, index, index+1)
	}
	replay.retention.release()
}
