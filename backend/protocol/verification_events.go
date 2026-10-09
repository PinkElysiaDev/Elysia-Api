package protocol

// A stream may split one item delta into several frames, or put its usage in
// the terminal rather than a tail. Compare complete, independently validated
// generations instead of requiring identical provider framing.
func compareEventSequence(compiled *Compiled, sample Sample, expected, actual []Event) error {
	if !sample.Sequence {
		return compareRoundTrip(compiled, sample, expected, actual)
	}
	before, err := collectVerificationSequence(compiled, sample, expected)
	if err != nil {
		return err
	}
	after, err := collectVerificationSequence(compiled, sample, actual)
	if err != nil {
		return err
	}
	return compareRoundTrip(compiled, sample, before, after)
}

func collectVerificationSequence(compiled *Compiled, sample Sample, events []Event) (Value, error) {
	// Each sequence retains its source capabilities for collection. Target
	// capability enforcement already happens while encoding and decoding.
	target := compiled.target(EncodeEvent, EvaluationContext{Scope: sample.Scope})
	if len(events) > 0 {
		target.Protocol = events[0].Source
	}
	collector, err := NewResponseCollector(target, compiled.limits)
	if err != nil {
		return Value{}, err
	}
	var failure *Event
	for _, event := range events {
		if event.Type == OperationFailed || event.Type == OperationCancelled || failure != nil {
			if _, err := collector.replay.Consume(event); err != nil {
				return Value{}, err
			}
			if event.Type != UsageUpdated {
				copy := event
				failure = &copy
			}
			continue
		}
		if _, _, err := collector.Consume(event); err != nil {
			return Value{}, err
		}
	}
	response, err := collector.Finish()
	if err != nil {
		return Value{}, err
	}
	response.Usage = comparableWireUsage(response.Usage)
	if failure != nil {
		return EncodeValue(struct {
			Type  EventType `json:"type"`
			Error Value     `json:"error"`
			Usage *Usage    `json:"usage,omitempty"`
		}{failure.Type, failure.Error, response.Usage})
	}
	// ResponseFinished already establishes completion even if a protocol has no
	// independent response status field. Item-local IDs are framing associations;
	// call IDs, order, resources and all payload attributes remain comparable.
	if response.Status.IsZero() {
		response.Status = StringValue("completed")
	}
	response.Content = comparableStreamOutput(response.Content)
	return comparableSemantic(response)
}

func comparableStreamOutput(nodes []Node) []Node {
	var output []Node
	for _, node := range nodes {
		node.Native = nil
		node.ID, node.Status = Value{}, Value{}
		node.Children = comparableStreamOutput(node.Children)
		if node.Kind == MessageNode && (node.Role.IsZero() || node.Role == StringValue("assistant")) && len(node.Attributes) == 0 && len(node.Cache) == 0 && len(node.Resources) == 0 && node.Payload.IsZero() && node.Input == nil && node.Name.IsZero() && node.CallID.IsZero() {
			if len(node.Children) == 0 {
				output = append(output, node)
				continue
			}
			for _, child := range node.Children {
				child.Metadata = MergeNodeMetadata(child.Metadata, node.Metadata, false)
				output = append(output, child)
			}
		} else {
			output = append(output, node)
		}
	}
	return output
}
