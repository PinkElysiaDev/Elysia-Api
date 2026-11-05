package protocol

import (
	"fmt"
	"strings"
)

type streamTrackedTool struct {
	kind                 InputKind
	callID               string
	text                 TextTracker
	input                strings.Builder
	bufferStart, emitted int
	isDone               bool
	isPlaceholder        bool
}

type pendingStreamTool struct {
	tool                               *streamTrackedTool
	kind                               InputKind
	callID                             string
	deltas                             []string
	deltaBytes, emitted, retainedBytes int
	isDone                             bool
}

type streamToolObservation struct {
	kind                                    InputKind
	callID                                  string
	text                                    string
	hasText, isSnapshot, isDone, shouldEmit bool
}

func readStreamToolObservation(output JSONOutput, reader *valueReader) (streamToolObservation, error) {
	var result streamToolObservation
	if plannedJSONType(output) != "object" {
		return result, fmt.Errorf("tool observation requires an object")
	}
	for _, field := range []struct {
		name   string
		target *bool
	}{{"snapshot", &result.isSnapshot}, {"done", &result.isDone}, {"emit", &result.shouldEmit}} {
		value, err := readOutputPointer(output, []string{field.name}, reader)
		if err != nil {
			return result, err
		}
		*field.target, err = readPlannedBoolean(value)
		if err != nil {
			return result, fmt.Errorf("tool %s: %w", field.name, err)
		}
	}
	for _, name := range []string{"kind", "callId", "text"} {
		value, err := readOutputPointer(output, []string{name}, reader)
		if err != nil {
			return result, err
		}
		if value.IsZero() {
			continue
		}
		text, err := readPlannedString(value)
		if err != nil {
			return result, err
		}
		switch name {
		case "kind":
			result.kind = InputKind(text)
		case "callId":
			result.callID = text
		case "text":
			result.text, result.hasText = text, true
		}
	}
	return result, nil
}

// contents is needed only for JSON snapshots/completion or withheld output.
// Ordinary argument deltas never rebuild their previously received prefix.
func (pending *pendingStreamTool) contents(extra string) string {
	if pending.tool.input.Len() == 0 && len(pending.deltas) == 0 {
		return extra
	}
	if extra == "" && len(pending.deltas) == 0 {
		return pending.tool.input.String()
	}
	var joined strings.Builder
	joined.Grow(pending.tool.input.Len() + pending.deltaBytes + len(extra))
	joined.WriteString(pending.tool.input.String())
	for _, delta := range pending.deltas {
		joined.WriteString(delta)
	}
	joined.WriteString(extra)
	return joined.String()
}

func (plan *compiledOutputPlan) trackStreamTool(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	observationOutput, err := plan.body.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	key, err := readPlannedString(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	observation, err := readStreamToolObservation(observationOutput.output, state.valueReader)
	if err != nil {
		return plannedOutput{}, err
	}
	transaction, channel := state.streamTransaction, streamTrackKey{channel: plan.expression.trackerName, item: key}
	base := transaction.primitives()
	pending := transaction.toolUpdates[channel]
	isNew := pending == nil && base.tools[channel] == nil
	if pending == nil {
		tool := base.tools[channel]
		if tool == nil {
			tool = &streamTrackedTool{kind: observation.kind}
		}
		pending = &pendingStreamTool{tool: tool, kind: tool.kind, callID: tool.callID, emitted: tool.emitted, retainedBytes: tool.input.Len(), isDone: tool.isDone}
	}
	if pending.kind != JSONInput && pending.kind != TextInput || observation.kind != "" && observation.kind != pending.kind {
		return plannedOutput{}, streamIssue(InvalidAssociation, key, "tool identity or input type changed during a call")
	}
	if pending.callID != "" && observation.callID != "" && pending.callID != observation.callID {
		return plannedOutput{}, streamIssue(InvalidAssociation, key, "tool identity or input type changed during a call")
	}
	if !pending.isDone && pending.tool.isPlaceholder && observation.hasText && (observation.text != "" || observation.isSnapshot) {
		// A declared initial placeholder owns no argument prefix once real input
		// arrives. Stage a fresh tracker so a rejected frame preserves the old one.
		transaction.toolBufferDelta -= pending.retainedBytes
		pending.tool = &streamTrackedTool{kind: pending.kind, callID: pending.callID}
		pending.retainedBytes, pending.emitted = 0, 0
	}
	callID := pending.callID
	if observation.callID != "" {
		callID = observation.callID
	}
	callKey := streamTrackKey{channel: channel.channel, item: callID}
	hasNewCall := false
	if callID != "" {
		previous := transaction.toolCallUpdates[callKey]
		if previous == "" {
			previous = base.toolCalls[callKey]
		}
		if previous != "" && previous != key {
			return plannedOutput{}, streamIssue(InvalidAssociation, key, "call ID is associated with multiple tool items")
		}
		hasNewCall = previous == ""
	}
	delta := observation.text
	if !observation.hasText {
		delta = ""
	}
	if pending.isDone {
		if !observation.isDone || observation.hasText && !observation.isSnapshot {
			return plannedOutput{}, streamIssue(UpstreamContractViolation, key, "tool input arrived after completion")
		}
		if observation.hasText {
			tracker := pendingStreamText{tracker: &pending.tool.text, deltas: pending.deltas}
			delta, err = tracker.snapshotSuffix(observation.text)
			if err != nil || delta != "" {
				if err == nil {
					err = streamIssue(UpstreamContractViolation, key, "tool input arrived after completion")
				}
				return plannedOutput{}, err
			}
		}
	} else if observation.hasText && observation.isSnapshot {
		if pending.kind == JSONInput {
			delta, err = JSONToolSnapshotSuffix(pending.contents(""), observation.text)
		} else {
			tracker := pendingStreamText{tracker: &pending.tool.text, deltas: pending.deltas}
			delta, err = tracker.snapshotSuffix(observation.text)
		}
		if err != nil {
			return plannedOutput{}, err
		}
	}
	received := pending.tool.text.Length() + pending.deltaBytes + len(delta)
	retained := received - pending.emitted
	if pending.kind == JSONInput {
		retained = pending.tool.input.Len() + pending.deltaBytes + len(delta)
	}
	callBytes := 0
	if hasNewCall {
		callBytes = len(callID)
	}
	if err := transaction.checkRetainedBytes(retained - pending.retainedBytes + callBytes); err != nil {
		return plannedOutput{}, err
	}
	var complete string
	hasComplete := false
	contents := func() string {
		if !hasComplete {
			complete, hasComplete = pending.contents(delta), true
		}
		return complete
	}
	if observation.isDone && !pending.isDone && pending.kind == JSONInput {
		if err := validateCompletedToolInput(key, pending.kind, contents()); err != nil {
			return plannedOutput{}, err
		}
	}
	suffix := ""
	if observation.shouldEmit {
		if pending.emitted == pending.tool.text.Length()+pending.deltaBytes {
			suffix = delta
		} else {
			suffix = contents()[pending.emitted-pending.tool.bufferStart:]
		}
		pending.emitted = received
	}
	pending.callID, pending.isDone = callID, pending.isDone || observation.isDone
	if pending.kind == TextInput {
		retained = received - pending.emitted
	} else if pending.isDone && pending.emitted == received {
		retained = 0
	}
	transaction.toolBufferDelta += retained - pending.retainedBytes
	pending.retainedBytes = retained
	if delta != "" {
		pending.deltas = append(pending.deltas, delta)
		pending.deltaBytes += len(delta)
	}
	if transaction.toolUpdates == nil {
		transaction.toolUpdates = make(map[streamTrackKey]*pendingStreamTool)
	}
	transaction.toolUpdates[channel] = pending
	if hasNewCall {
		if transaction.toolCallUpdates == nil {
			transaction.toolCallUpdates = make(map[streamTrackKey]string)
		}
		transaction.toolCallUpdates[callKey] = key
		transaction.toolCallBytes += callBytes
	}
	if isNew {
		if err := transaction.reserveTrackChannel(key); err != nil {
			return plannedOutput{}, err
		}
	}
	return plannedLeaf(StringValue(suffix)), nil
}

func (transaction *packageStreamTransaction) commitTools(base *packageStreamPrimitives) {
	if len(transaction.toolUpdates) == 0 {
		return
	}
	if base.tools == nil {
		base.tools, base.toolCalls = make(map[streamTrackKey]*streamTrackedTool), make(map[streamTrackKey]string)
	}
	for key, pending := range transaction.toolUpdates {
		tool := pending.tool
		key.item = base.trackedItems[key.item].key
		if pending.retainedBytes == 0 {
			tool.input.Reset()
			tool.bufferStart = tool.text.Length() + pending.deltaBytes
		} else if pending.kind == TextInput && pending.emitted > tool.bufferStart {
			suffix := pending.contents("")[pending.emitted-tool.bufferStart:]
			tool.input.Reset()
			tool.input.WriteString(suffix)
			tool.bufferStart = pending.emitted
		} else {
			for _, delta := range pending.deltas {
				tool.input.WriteString(delta)
			}
		}
		for _, delta := range pending.deltas {
			tool.text.Append(delta)
		}
		if tool.callID != pending.callID {
			tool.callID = strings.Clone(pending.callID)
		}
		tool.isDone, tool.emitted = pending.isDone, pending.emitted
		base.tools[key] = tool
	}
	for key, item := range transaction.toolCallUpdates {
		base.toolCalls[streamTrackKey{channel: key.channel, item: strings.Clone(key.item)}] = strings.Clone(item)
	}
	base.toolBuffered += transaction.toolBufferDelta
}
