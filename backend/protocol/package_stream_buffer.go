package protocol

import (
	"context"
	"fmt"
	"strings"
)

// Fragments own only the selected payload. A text read coalesces their encoded
// string contents; commit then releases the old fragments. Arrays retain scalar
// Value storage shared by their immutable construction snapshot.
type streamBuffer struct {
	fragments                         []Value
	textTail                          strings.Builder
	snapshot                          JSONOutput
	length, bytes, nodes, outputBytes int
}

type pendingStreamBuffer struct {
	previous                          *streamBuffer
	fragments                         []Value
	snapshot                          JSONOutput
	length, bytes, nodes, outputBytes int
	isText, isReleased                bool
}

func (plan *compiledOutputPlan) useStreamBuffer(state evaluation) (plannedOutput, error) {
	selected, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	key, err := readPlannedString(selected.output)
	if err != nil || key == "" {
		return plannedOutput{}, streamIssue(InvalidAssociation, "/buffer", "buffer requires a nonempty item identity")
	}
	transaction := state.streamTransaction
	base := transaction.primitives()
	channel := streamTrackKey{channel: plan.expression.op + ":" + plan.expression.trackerName, item: key}
	pending := transaction.bufferUpdates[channel]
	if pending == nil || pending.isReleased {
		previous := base.buffers[channel]
		if pending != nil {
			previous = nil
		}
		operation := plan.expression.operation
		if previous == nil && operation != "append" && operation != "snapshot" {
			return plannedOutput{}, streamIssue(InvalidAssociation, "/buffer", "buffer operation requires an initialized item")
		}
		pending = &pendingStreamBuffer{previous: previous, isText: plan.expression.op == "buffer_text"}
		if previous != nil {
			pending.snapshot, pending.length, pending.bytes = previous.snapshot, previous.length, previous.bytes
			pending.nodes, pending.outputBytes = previous.nodes, previous.outputBytes
		} else if err := transaction.reserveTrackChannel(key); err != nil {
			return plannedOutput{}, err
		}
		if transaction.bufferUpdates == nil {
			transaction.bufferUpdates = make(map[streamTrackKey]*pendingStreamBuffer)
		}
		transaction.bufferUpdates[channel] = pending
	}
	switch plan.expression.operation {
	case "length":
		return plannedLeaf(coreInteger(int64(pending.length))), nil
	case "release":
		transaction.bufferByteDelta -= pending.bytes
		transaction.releaseTrackChannel(key)
		pending.isReleased = true
		pending.fragments, pending.snapshot = nil, JSONOutput{}
		return plannedOutput{}, nil
	case "read":
		return pending.read(state)
	case "append", "snapshot":
		value, err := plan.body.buildNode(state)
		if err != nil {
			return plannedOutput{}, err
		}
		if pending.isText {
			return pending.appendText(state, value.output, plan.expression.operation == "snapshot")
		}
		return pending.appendArray(state, value.output, plan.expression.operation == "snapshot")
	}
	panic("uncompiled buffer operation")
}

func (pending *pendingStreamBuffer) eachFragment(ctx context.Context, visit func(Value) error) error {
	if pending.previous != nil {
		for _, value := range pending.previous.fragments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(value); err != nil {
				return err
			}
		}
	}
	for _, value := range pending.fragments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(value); err != nil {
			return err
		}
	}
	return nil
}

func (pending *pendingStreamBuffer) eachEncodedText(ctx context.Context, visit func(string) error) error {
	if pending.previous != nil {
		for _, value := range pending.previous.fragments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(value.raw[1 : len(value.raw)-1]); err != nil {
				return err
			}
		}
		if pending.previous.textTail.Len() != 0 {
			if err := visit(pending.previous.textTail.String()); err != nil {
				return err
			}
		}
	}
	for _, value := range pending.fragments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(value.raw[1 : len(value.raw)-1]); err != nil {
			return err
		}
	}
	return nil
}

func (pending *pendingStreamBuffer) appendText(state evaluation, output JSONOutput, isSnapshot bool) (plannedOutput, error) {
	text, err := readPlannedString(output)
	if err != nil {
		return plannedOutput{}, err
	}
	if isSnapshot {
		if err := pending.eachEncodedText(state.ctx, func(encoded string) error {
			prefix, err := readString(Value{raw: `"` + encoded + `"`})
			if err != nil {
				return err
			}
			if !strings.HasPrefix(text, prefix) {
				return streamIssue(UpstreamContractViolation, "/buffer", "text snapshot changed the retained prefix")
			}
			text = text[len(prefix):]
			return nil
		}); err != nil {
			return plannedOutput{}, err
		}
	}
	value := StringValue(text)
	if text == "" {
		return plannedLeaf(value), nil
	}
	bytes := value.Len() - 2
	if err := state.streamTransaction.checkRetainedBytes(bytes); err != nil {
		return plannedOutput{}, err
	}
	pending.fragments = append(pending.fragments, value)
	pending.length += len(text)
	pending.bytes += bytes
	pending.snapshot = JSONOutput{}
	state.streamTransaction.bufferByteDelta += bytes
	return plannedLeaf(value), nil
}

func (pending *pendingStreamBuffer) appendArray(state evaluation, output JSONOutput, isSnapshot bool) (plannedOutput, error) {
	items, err := plannedArrayItems(output)
	if err != nil {
		return plannedOutput{}, err
	}
	offset := 0
	if isSnapshot {
		current := func(index int) (Value, error) { return items[index].Encode(state.ctx) }
		if pending.previous != nil {
			offset, err = verifyArrayPrefix(state.ctx, pending.previous.fragments, len(items), offset, current)
			if err != nil {
				return plannedOutput{}, err
			}
		}
		offset, err = verifyArrayPrefix(state.ctx, pending.fragments, len(items), offset, current)
		if err != nil {
			return plannedOutput{}, err
		}
	}
	added := make([]JSONOutput, 0, len(items)-offset)
	for _, item := range items[offset:] {
		value, err := item.Encode(state.ctx)
		if err != nil {
			return plannedOutput{}, err
		}
		meter := valueTokenMeter{limits: state.budget.limits, domain: "stream_buffer", depth: 1, nodes: pending.nodes + 2}
		if err := meter.scan(value.raw); err != nil {
			return plannedOutput{}, err
		}
		if err := state.streamTransaction.checkRetainedBytes(value.Len()); err != nil {
			return plannedOutput{}, err
		}
		if item.node == nil {
			value.raw = strings.Clone(value.raw)
		}
		leaf := JSONLeaf(value)
		pending.fragments = append(pending.fragments, value)
		pending.length++
		pending.bytes += value.Len()
		pending.nodes = meter.nodes - 2
		pending.outputBytes += normalizedOutputBytes(leaf)
		state.streamTransaction.bufferByteDelta += value.Len()
		added = append(added, leaf)
	}
	if len(added) != 0 {
		pending.snapshot = JSONOutput{}
	}
	result := JSONOutput{node: &jsonOutputNode{items: added}}
	return plannedOutput{output: result, bytes: normalizedOutputBytes(result)}, nil
}

func (pending *pendingStreamBuffer) read(state evaluation) (plannedOutput, error) {
	bytes := pending.outputBytes + 2 + max(0, pending.length-1)
	if pending.isText {
		bytes = pending.bytes + 2
	}
	if !pending.snapshot.IsZero() {
		return plannedOutput{output: pending.snapshot, bytes: bytes}, nil
	}
	if pending.isText {
		var text strings.Builder
		text.Grow(bytes)
		text.WriteByte('"')
		if err := pending.eachEncodedText(state.ctx, func(encoded string) error {
			text.WriteString(encoded)
			return nil
		}); err != nil {
			return plannedOutput{}, err
		}
		text.WriteByte('"')
		pending.snapshot = JSONLeaf(Value{raw: text.String()})
	} else {
		items := make([]JSONOutput, 0, pending.length)
		if err := pending.eachFragment(state.ctx, func(value Value) error {
			items = append(items, JSONLeaf(value))
			return nil
		}); err != nil {
			return plannedOutput{}, err
		}
		pending.snapshot = JSONOutput{node: &jsonOutputNode{items: items}}
	}
	return plannedOutput{output: pending.snapshot, bytes: bytes}, nil
}

func (transaction *packageStreamTransaction) releaseTrackChannel(key string) {
	base := transaction.primitives()
	remaining := base.trackedItems[key].channels + transaction.trackChannels[key] - 1
	if remaining < 0 {
		panic("release of an unregistered tracker channel")
	}
	if transaction.trackChannels == nil {
		transaction.trackChannels = make(map[string]int)
	}
	transaction.trackChannels[key]--
	if remaining == 0 {
		transaction.trackedBytes -= len(key)
		if readItemAlias(base.identities.aliases, transaction.aliasUpdates, key) == "" {
			transaction.primitiveItems--
		}
	}
}

func (transaction *packageStreamTransaction) commitBuffers(base *packageStreamPrimitives) {
	if len(transaction.bufferUpdates) == 0 {
		return
	}
	if base.buffers == nil {
		base.buffers = make(map[streamTrackKey]*streamBuffer)
	}
	for key, pending := range transaction.bufferUpdates {
		if pending.isReleased {
			delete(base.buffers, key)
			continue
		}
		buffer := pending.previous
		if buffer == nil {
			buffer = &streamBuffer{}
		}
		key.item = base.trackedItems[key.item].key
		if pending.isText && !pending.snapshot.IsZero() {
			buffer.fragments = []Value{pending.snapshot.value}
			buffer.textTail.Reset()
		} else if pending.isText {
			for _, value := range pending.fragments {
				buffer.textTail.WriteString(value.raw[1 : len(value.raw)-1])
			}
		} else {
			buffer.fragments = append(buffer.fragments, pending.fragments...)
		}
		buffer.snapshot, buffer.length, buffer.bytes = pending.snapshot, pending.length, pending.bytes
		buffer.nodes, buffer.outputBytes = pending.nodes, pending.outputBytes
		base.buffers[key] = buffer
	}
	base.buffered += transaction.bufferByteDelta
}

func compileBufferOperation(expression *compiledExpression) error {
	if !isAssignable(expression.source.result, StringType) {
		return fmt.Errorf("buffer requires a string item key")
	}
	kind := StringType
	if expression.op == "buffer_array" {
		kind = ArrayType
	}
	switch expression.operation {
	case "append", "snapshot":
		if expression.body == nil || !isAssignable(expression.body.result, kind) {
			return fmt.Errorf("buffer update requires a %s body", kind)
		}
		expression.result = kind
	case "read", "release", "length":
		if expression.body != nil {
			return fmt.Errorf("buffer observation cannot declare a body")
		}
		expression.result = kind
		if expression.operation == "length" {
			expression.result = IntegerType
		}
		if expression.operation == "release" {
			expression.result = AnyType
		}
	default:
		return fmt.Errorf("unknown buffer operation %q", expression.operation)
	}
	return nil
}
