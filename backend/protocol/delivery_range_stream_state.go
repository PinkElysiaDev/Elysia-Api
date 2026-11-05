package protocol

import (
	"sort"
	"strings"
)

type deliveryRangeKey struct {
	channel int
	text    string
}

// Text observations retain digests, positions and an independently owned key.
// Source and delivered text share their digest until the first actual rewrite.
type deliveryStreamText struct {
	key       string
	source    *TextTracker
	output    *TextTracker
	positions deliveryTextPositions
}

type deliveryRangeStream struct {
	texts                     map[deliveryRangeKey]*deliveryStreamText
	pending                   map[deliveryRangeKey]*pendingDeliveryText
	items, bytes, stagedBytes int
}

// A transaction copies only the mutable tail span. Earlier intervals remain
// immutable and are searched in place, including after arbitrarily many edits.
type pendingDeliveryPositions struct {
	previous *deliveryTextPositions
	next     deliveryTextPositions
	base     int
	tail     [3]streamPositionSpan
}

func (pending *pendingDeliveryPositions) begin(previous *deliveryTextPositions) {
	pending.previous = previous
	pending.next = *previous
	pending.base = len(previous.spans)
	pending.next.spans = pending.tail[:0]
	if pending.base > 0 {
		pending.base--
		pending.next.spans = append(pending.next.spans, previous.spans[pending.base])
	}
}

func (pending *pendingDeliveryPositions) span(index int) streamPositionSpan {
	if index < pending.base {
		return pending.previous.spans[index]
	}
	return pending.next.spans[index-pending.base]
}

func (pending *pendingDeliveryPositions) project(start, end int64) (int64, int64, error) {
	if start < 0 || end < start || end > pending.next.source {
		return 0, 0, deliveryRangeIssue(InvalidAssociation, "/range", "reference exceeds observed source text")
	}
	if !pending.next.diverged {
		return start, end, nil
	}
	count := pending.base + len(pending.next.spans)
	index := sort.Search(count, func(index int) bool {
		span := pending.span(index)
		return span.local+span.length >= end
	})
	if index == count || pending.span(index).local > start {
		return 0, 0, deliveryRangeIssue(ConversionRejected, "/range", "reference intersects rewritten or unproven text")
	}
	span := pending.span(index)
	offset := span.output - span.local
	return start + offset, end + offset, nil
}

func (pending *pendingDeliveryPositions) commit() {
	previous := pending.previous
	if pending.base < len(previous.spans) {
		previous.spans[pending.base] = pending.next.spans[0]
		previous.spans = append(previous.spans, pending.next.spans[1:]...)
	} else {
		previous.spans = append(previous.spans, pending.next.spans...)
	}
	previous.source, previous.output, previous.diverged = pending.next.source, pending.next.output, pending.next.diverged
}

type pendingDeliveryText struct {
	previous  *deliveryStreamText
	positions pendingDeliveryPositions
	source    pendingStreamText
	output    pendingStreamText
}

func (stream *deliveryRangeStream) edit(key deliveryRangeKey) *pendingDeliveryText {
	if pending := stream.pending[key]; pending != nil {
		return pending
	}
	previous := stream.texts[key]
	isNew := previous == nil
	if isNew {
		previous = &deliveryStreamText{key: strings.Clone(key.text), source: &TextTracker{}}
		previous.output = previous.source
	}
	key.text = previous.key
	pending := &pendingDeliveryText{previous: previous,
		source: pendingStreamText{tracker: previous.source}, output: pendingStreamText{tracker: previous.output}}
	pending.positions.begin(&previous.positions)
	if stream.pending == nil {
		stream.pending = make(map[deliveryRangeKey]*pendingDeliveryText)
	}
	stream.pending[key] = pending
	if isNew {
		stream.items++
		stream.bytes += len(previous.key)
	}
	return pending
}

func (pending *pendingDeliveryText) observe(before, after deliveryTextObservation, measure func(string) (int64, error)) (int, error) {
	if before.mode != after.mode {
		return 0, deliveryRangeIssue(InvalidAssociation, after.path, "wire edit changed the text observation mode")
	}
	source, output := before.text, after.text
	if before.mode == "snapshot" {
		var err error
		source, err = pending.source.snapshotSuffix(source)
		if err != nil {
			return 0, deliveryRangeIssue(ConversionRejected, before.path, "source snapshot rewrote an already observed prefix")
		}
		output, err = pending.output.snapshotSuffix(output)
		if err != nil {
			return 0, deliveryRangeIssue(ConversionRejected, after.path, "output snapshot rewrote an already delivered prefix")
		}
	}
	if err := pending.positions.next.append(source, output, measure); err != nil {
		return 0, err
	}
	if source == output {
		output = source
	}
	if source != "" {
		pending.source.deltas = append(pending.source.deltas, source)
	}
	if output != "" {
		pending.output.deltas = append(pending.output.deltas, output)
	}
	if source == output {
		return len(source), nil
	}
	return len(source) + len(output), nil
}

func (stream *deliveryRangeStream) commit() error {
	if len(stream.pending) == 0 {
		return nil
	}
	if stream.texts == nil {
		stream.texts = make(map[deliveryRangeKey]*deliveryStreamText, len(stream.pending))
	}
	for key, pending := range stream.pending {
		previous := pending.previous
		if previous.output == previous.source && pending.positions.next.diverged {
			copy, err := cloneTextTracker(previous.source)
			if err != nil {
				return err
			}
			previous.output = copy
		}
		for _, delta := range pending.source.deltas {
			previous.source.Append(delta)
		}
		if previous.output != previous.source {
			for _, delta := range pending.output.deltas {
				previous.output.Append(delta)
			}
		}
		pending.positions.commit()
		stream.texts[key] = previous
	}
	clear(stream.pending)
	stream.stagedBytes = 0
	return nil
}
