package protocol

import "strings"

// ResponseCollector assembles a bounded result for consumers that need a whole
// response (Agent and probes). Ordinary gateway streams use EventReplay alone.
type ResponseCollector struct {
	replay   *EventReplay
	response Response
	items    map[string]int
	texts    map[string]*strings.Builder
	bytes    int
	limit    int
}

// NewResponseCollector uses the same lifecycle validator as live forwarding.
func NewResponseCollector(target Target, limits Limits) (*ResponseCollector, error) {
	replay, err := NewEventReplay(target, limits)
	if err != nil {
		return nil, err
	}
	return &ResponseCollector{replay: replay, response: Response{SchemaVersion: SemanticSchemaVersion, Source: target.Protocol}, items: map[string]int{}, texts: map[string]*strings.Builder{}, limit: limits.BufferBytes}, nil
}

// Consume returns the newly appended text, if any. Snapshot prefixes and
// duplicate sequence frames never appear twice in a caller's output.
func (collector *ResponseCollector) Consume(event Event) (NodeKind, string, error) {
	isAccepted, err := collector.replay.Consume(event)
	if err != nil || !isAccepted {
		return "", "", err
	}
	if !event.ResponseID.IsZero() {
		collector.response.ID = event.ResponseID
	}
	switch event.Type {
	case OperationFailed, OperationCancelled:
		return "", "", streamIssue(UpstreamContractViolation, "/type", "generation ended with "+string(event.Type))
	case ResponseFinished:
		if err := collector.finishTools(); err != nil {
			return "", "", err
		}
		if event.Response != nil {
			if len(event.Response.Content) > 0 && len(collector.items) > 0 {
				actual, err := EncodeValue(comparableNodes(collector.response.Content))
				if err != nil {
					return "", "", err
				}
				expected, err := EncodeValue(comparableNodes(event.Response.Content))
				if err != nil {
					return "", "", err
				}
				if !equalValues(actual, expected) {
					return "", "", streamIssue(UpstreamContractViolation, "/response/content", "terminal content differs from streamed items")
				}
			}
			content := collector.response.Content
			collector.response = *event.Response
			if len(collector.response.Content) == 0 {
				collector.response.Content = content
			}
			encoded, err := EncodeValue(collector.response)
			if err != nil {
				return "", "", err
			}
			if len(encoded.Bytes()) > collector.limit {
				return "", "", streamIssue(LimitExceeded, "/response", "collected response exceeds buffer limit")
			}
		}
		return "", "", nil
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		return collector.collectItem(event)
	default:
		return "", "", nil
	}
}

func (collector *ResponseCollector) collectItem(event Event) (NodeKind, string, error) {
	key, err := collector.replay.itemKey(event)
	if err != nil {
		return "", "", err
	}
	if event.Type == ItemStarted {
		metadata, err := EncodeValue(event.Item)
		if err != nil {
			return "", "", err
		}
		if collector.bytes+len(metadata.Bytes()) > collector.limit {
			return "", "", streamIssue(LimitExceeded, "/item", "collected item metadata exceeds buffer limit")
		}
		collector.bytes += len(metadata.Bytes())
		collector.items[key] = len(collector.response.Content)
		collector.response.Content = append(collector.response.Content, *event.Item)
		collector.texts[key] = &strings.Builder{}
	}
	item := &collector.response.Content[collector.items[key]]
	if event.Item != nil {
		if !event.Item.Name.IsZero() {
			item.Name = event.Item.Name
		}
		if !event.Item.CallID.IsZero() {
			item.CallID = event.Item.CallID
		}
		if !event.Item.Status.IsZero() {
			item.Status = event.Item.Status
		}
	}
	if !event.CallID.IsZero() {
		item.CallID = event.CallID
	}
	value := event.Delta
	isSnapshot := event.Type != ItemDelta
	if isSnapshot && event.Item != nil {
		value = event.Item.Payload
		if event.Item.Input != nil {
			value = event.Item.Input.Value
		}
	}
	buffer := collector.texts[key]
	var delta string
	if !value.IsZero() {
		if item.Kind == ToolCallNode && isSnapshot && item.Input.Kind == JSONInput {
			delta = string(value.Bytes())
		} else if err := value.Decode(&delta); err != nil {
			return "", "", err
		}
		if isSnapshot {
			delta = delta[buffer.Len():]
		} // Replay already verified the prefix.
		if collector.bytes+len(delta) > collector.limit {
			return "", "", streamIssue(LimitExceeded, "/response", "collected response exceeds buffer limit")
		}
		buffer.WriteString(delta)
		collector.bytes += len(delta)
	}
	if item.Kind != ToolCallNode {
		item.Payload = StringValue(buffer.String())
		return item.Kind, delta, nil
	}
	if event.Type == ItemFinished {
		input := StringValue(buffer.String())
		if item.Input.Kind == JSONInput {
			input, err = ParseValue([]byte(buffer.String()))
			if err != nil {
				return "", "", err
			}
		}
		item.Input = &ToolInput{Kind: item.Input.Kind, Value: input}
	}
	return item.Kind, "", nil
}

// Finish requires a real terminal and includes usage arriving after completion.
func (collector *ResponseCollector) Finish() (*Response, error) {
	if err := collector.replay.Finish(); err != nil {
		return nil, err
	}
	collector.response.Usage = collector.replay.Usage()
	return &collector.response, nil
}

func (collector *ResponseCollector) finishTools() error {
	for key, index := range collector.items {
		item := &collector.response.Content[index]
		if item.Kind != ToolCallNode || collector.replay.items[key].isFinished {
			continue
		}
		input := StringValue(collector.texts[key].String())
		if item.Input.Kind == JSONInput {
			var err error
			input, err = ParseValue([]byte(collector.texts[key].String()))
			if err != nil {
				return err
			}
		}
		item.Input = &ToolInput{Kind: item.Input.Kind, Value: input}
	}
	return nil
}
