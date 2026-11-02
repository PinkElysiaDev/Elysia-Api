package protocol

import (
	"math"
	"sort"
)

// Spans contain only positions in the declared unit, never forwarded text.
// Adjacent appends coalesce when both source and output positions are adjacent.
type streamPositionSpan struct{ local, output, length int64 }

type streamPositionTracker struct {
	length int64
	spans  []streamPositionSpan
}

// Only the last committed span can grow. Stage that growth separately so a
// failed event cannot alter earlier state or copy an ever-growing span prefix.
type pendingStreamPositions struct {
	previous  *streamPositionTracker
	length    int64
	tailDelta int64
	appended  []streamPositionSpan
}

func (pending *pendingStreamPositions) spanCount() int {
	return len(pending.previous.spans) + len(pending.appended)
}

func (pending *pendingStreamPositions) span(index int) streamPositionSpan {
	if index >= len(pending.previous.spans) {
		return pending.appended[index-len(pending.previous.spans)]
	}
	span := pending.previous.spans[index]
	if index == len(pending.previous.spans)-1 {
		span.length += pending.tailDelta
	}
	return span
}

func (pending *pendingStreamPositions) append(output, length int64) bool {
	count := pending.spanCount()
	if count != 0 {
		last := pending.span(count - 1)
		if last.output+last.length == output {
			if len(pending.appended) != 0 {
				pending.appended[len(pending.appended)-1].length += length
			} else {
				pending.tailDelta += length
			}
			pending.length += length
			return false
		}
	}
	pending.appended = append(pending.appended, streamPositionSpan{local: pending.length, output: output, length: length})
	pending.length += length
	return true
}

func (plan *compiledOutputPlan) positionItem(state evaluation) (streamTrackKey, error) {
	value, err := plan.source.build(state)
	if err != nil {
		return streamTrackKey{}, err
	}
	key, err := readPlannedString(value.output)
	return streamTrackKey{channel: plan.expression.trackerName, item: key}, err
}

func (plan *compiledOutputPlan) trackStreamPosition(state evaluation) (plannedOutput, error) {
	key, err := plan.positionItem(state)
	if err != nil {
		return plannedOutput{}, err
	}
	value, err := plan.body.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	length, err := readPlannedInteger(value.output)
	if err != nil || length < 0 {
		return plannedOutput{}, streamIssue(InvalidInput, "/length", "position tracking requires a nonnegative integer length")
	}
	transaction := state.streamTransaction
	base := transaction.primitives()
	output, hasPending := transaction.positionEnds[key.channel]
	if !hasPending {
		output = base.positionEnds[key.channel]
	}
	if length > math.MaxInt64-output {
		return plannedOutput{}, streamIssue(LimitExceeded, "/length", "stream position exceeds the integer range")
	}
	// Repeated empty observations do not move an item's origin. The first one
	// records its empty range, which a later annotation can still address.
	if length == 0 && (transaction.positionUpdates[key] != nil || base.positions[key] != nil) {
		return plannedLeaf(coreInteger(output)), nil
	}
	pending := transaction.positionUpdates[key]
	if pending == nil {
		previous := base.positions[key]
		if previous == nil {
			if err := transaction.reserveTrackChannel(key.item, state.budget.limits); err != nil {
				return plannedOutput{}, err
			}
			previous = &streamPositionTracker{}
		}
		pending = &pendingStreamPositions{previous: previous, length: previous.length}
		if transaction.positionUpdates == nil {
			transaction.positionUpdates = make(map[streamTrackKey]*pendingStreamPositions)
			transaction.positionEnds = make(map[string]int64)
		}
		transaction.positionUpdates[key] = pending
	}
	if pending.append(output, length) {
		transaction.positionItems++
		items := len(base.trackedItems) + transaction.trackItems + base.positionItems + transaction.positionItems
		if items > state.budget.limits.StateItems {
			return plannedOutput{}, resourceFailure("stream", "position_items", state.budget.limits.StateItems, items)
		}
	}
	transaction.positionEnds[key.channel] = output + length
	return plannedLeaf(coreInteger(output + length)), nil
}

func (plan *compiledOutputPlan) projectStreamRange(state evaluation) (plannedOutput, error) {
	key, err := plan.positionItem(state)
	if err != nil {
		return plannedOutput{}, err
	}
	value, err := plan.body.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	var bounds [2]int64
	for index, field := range []string{"start", "end"} {
		value, err := readOutputPointer(value.output, []string{field}, state.valueReader)
		if err != nil {
			return plannedOutput{}, err
		}
		bounds[index], err = readPlannedInteger(value)
		if err != nil || bounds[index] < 0 {
			return plannedOutput{}, streamIssue(InvalidInput, "/"+field, "range bounds must be nonnegative integers")
		}
	}
	if bounds[0] > bounds[1] {
		return plannedOutput{}, streamIssue(InvalidInput, "/start", "range start exceeds its end")
	}
	transaction := state.streamTransaction
	pending := transaction.positionUpdates[key]
	if pending == nil {
		previous := transaction.primitives().positions[key]
		if previous == nil {
			return plannedOutput{}, streamIssue(InvalidAssociation, "/range", "range has no observed source positions")
		}
		pending = &pendingStreamPositions{previous: previous, length: previous.length}
	}
	index := sort.Search(pending.spanCount(), func(index int) bool {
		span := pending.span(index)
		return span.local+span.length >= bounds[1]
	})
	if index == pending.spanCount() || pending.span(index).local > bounds[0] {
		return plannedOutput{}, streamIssue(UnsupportedCapability, "/range", "range exceeds observed text or crosses another item's output")
	}
	span := pending.span(index)
	offset := span.output - span.local
	output := JSONObject(map[string]JSONOutput{"start": JSONLeaf(coreInteger(bounds[0] + offset)), "end": JSONLeaf(coreInteger(bounds[1] + offset))})
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (transaction *packageStreamTransaction) commitPositions(base *packageStreamPrimitives) {
	if len(transaction.positionUpdates) == 0 {
		return
	}
	if base.positions == nil {
		base.positions = make(map[streamTrackKey]*streamPositionTracker)
		base.positionEnds = make(map[string]int64)
	}
	for key, pending := range transaction.positionUpdates {
		tracker := pending.previous
		if pending.tailDelta != 0 {
			tracker.spans[len(tracker.spans)-1].length += pending.tailDelta
		}
		tracker.spans = append(tracker.spans, pending.appended...)
		tracker.length = pending.length
		key.item = base.trackedItems[key.item].key
		base.positions[key] = tracker
	}
	for channel, end := range transaction.positionEnds {
		base.positionEnds[channel] = end
	}
	base.positionItems += transaction.positionItems
}
