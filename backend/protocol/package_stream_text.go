package protocol

import "strings"

type streamTrackKey struct{ channel, item string }

type pendingStreamText struct {
	tracker *TextTracker
	deltas  []string
}

func (pending *pendingStreamText) snapshotSuffix(snapshot string) (string, error) {
	suffix, err := pending.tracker.snapshotSuffix(snapshot)
	if err != nil {
		return "", err
	}
	for _, delta := range pending.deltas {
		if !strings.HasPrefix(suffix, delta) {
			return "", streamIssue(UpstreamContractViolation, "snapshot", "cumulative content rewrote a staged prefix")
		}
		suffix = suffix[len(delta):]
	}
	return suffix, nil
}

func (plan *compiledOutputPlan) trackStreamText(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	textOutput, err := plan.body.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	key, err := readPlannedString(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	text, err := readPlannedString(textOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	transaction := state.streamTransaction
	base := transaction.primitives()
	channel := streamTrackKey{channel: plan.expression.trackerName, item: key}
	pending := transaction.textUpdates[channel]
	isNew := pending == nil && base.texts[channel] == nil
	if pending == nil {
		tracker := base.texts[channel]
		if tracker == nil {
			tracker = &TextTracker{}
		}
		pending = &pendingStreamText{tracker: tracker}
	}
	delta := text
	if plan.expression.policy == "snapshot" {
		delta, err = pending.snapshotSuffix(text)
		if err != nil {
			return plannedOutput{}, err
		}
	}
	if delta != "" {
		pending.deltas = append(pending.deltas, delta)
	}
	if isNew {
		if err := transaction.reserveTrackChannel(key); err != nil {
			return plannedOutput{}, err
		}
	}
	if transaction.textUpdates == nil {
		transaction.textUpdates = make(map[streamTrackKey]*pendingStreamText)
	}
	transaction.textUpdates[channel] = pending
	return plannedLeaf(StringValue(delta)), nil
}
