package protocol

import "strings"

// ResponseCollector assembles a bounded result for consumers that need a whole
// response (Agent and probes). Ordinary gateway streams use EventReplay alone.
type ResponseCollector struct {
	replay   *EventReplay
	response Response
	items    map[string]int
	texts    map[string]*strings.Builder
	metadata map[string]int
	bytes    int
	limit    int
}

// NewResponseCollector uses the same lifecycle validator as live forwarding.
func NewResponseCollector(target Target, limits Limits) (*ResponseCollector, error) {
	replay, err := NewEventReplay(target, limits)
	if err != nil {
		return nil, err
	}
	return &ResponseCollector{replay: replay, response: Response{SchemaVersion: SemanticSchemaVersion, Source: target.Protocol}, items: map[string]int{}, texts: map[string]*strings.Builder{}, metadata: map[string]int{}, limit: limits.BufferBytes}, nil
}

// Consume returns the newly appended text, if any. Snapshot prefixes and
// duplicate sequence frames never appear twice in a caller's output.
func (collector *ResponseCollector) Consume(event Event) (NodeKind, string, error) {
	if event.Unmapped != nil {
		return "", "", streamIssue(UnsupportedNative, "/unmapped", "response collection cannot discard unmapped stream fields")
	}
	isAccepted, err := collector.replay.Consume(event)
	if err != nil || !isAccepted {
		return "", "", err
	}
	if !event.ResponseID.IsZero() {
		collector.response.ID = event.ResponseID
	}
	if event.Response != nil {
		if err := CheckGenerationOutcome(event.Response); err != nil {
			return "", "", err
		}
	}
	switch event.Type {
	case ResponseStarted:
		if event.Response != nil {
			collector.response = *event.Response
		}
		return "", "", nil
	case NativeEvent, MediaReceived:
		return "", "", streamIssue(UnsupportedCapability, "/type", "bounded response collection cannot represent native or realtime media events")
	case OperationFailed, OperationCancelled:
		return "", "", streamIssue(UpstreamContractViolation, "/type", "generation ended with "+string(event.Type))
	case ResponseFinished:
		if err := collector.finishTools(); err != nil {
			return "", "", err
		}
		if event.Response != nil {
			if len(event.Response.Content) > 0 && len(collector.items) > 0 {
				actual, err := EncodeValue(collectedOutput(collector.response.Content))
				if err != nil {
					return "", "", err
				}
				expected, err := EncodeValue(collectedOutput(event.Response.Content))
				if err != nil {
					return "", "", err
				}
				if !equalValues(actual, expected) {
					return "", "", streamIssue(UpstreamContractViolation, "/response/content", "terminal content differs from streamed items")
				}
			}
			content, id, model := collector.response.Content, collector.response.ID, collector.response.Model
			collector.response = *event.Response
			if collector.response.ID.IsZero() {
				collector.response.ID = id
			}
			if collector.response.Model.IsZero() {
				collector.response.Model = model
			}
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

// A terminal may wrap streamed leaves in output messages. Compare generated
// payloads here; the returned terminal still retains its complete identities
// and native attributes. Wire fidelity is verified separately by frame replay.
func collectedOutput(nodes []Node) []Node {
	var output []Node
	for _, node := range nodes {
		if node.Kind == MessageNode {
			output = append(output, collectedOutput(node.Children)...)
			continue
		}
		node.Native = nil
		node.Source = nil
		node.ID, node.Status = Value{}, Value{}
		node.Children = comparableNodes(node.Children)
		output = append(output, node)
	}
	return output
}

func (collector *ResponseCollector) collectItem(event Event) (NodeKind, string, error) {
	key, err := collector.replay.itemKey(event)
	if err != nil {
		return "", "", err
	}
	if event.Type == ItemStarted {
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
		if !event.Item.ID.IsZero() {
			item.ID = event.Item.ID
		}
		if event.Item.Resources != nil {
			item.Resources = append([]Resource(nil), event.Item.Resources...)
		}
		if event.Item.Children != nil {
			item.Children = append([]Node(nil), event.Item.Children...)
		}
		if event.Item.Cache != nil {
			item.Cache = append([]CacheIntent(nil), event.Item.Cache...)
		}
		if event.Item.Attributes != nil {
			item.Attributes = make(Object, len(event.Item.Attributes))
			for key, value := range event.Item.Attributes {
				item.Attributes[key] = value
			}
		}
	}
	if !event.CallID.IsZero() {
		item.CallID = event.CallID
	}
	if event.Item != nil || !event.CallID.IsZero() {
		if err := collector.accountItemMetadata(key, *item); err != nil {
			return "", "", err
		}
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
		if item.ReasoningForm == "summary" {
			return item.Kind, "", nil
		}
		for _, resource := range item.Resources {
			if resource.Kind == "encrypted_content" && item.Payload.IsZero() {
				return item.Kind, "", nil
			}
		}
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

func (collector *ResponseCollector) accountItemMetadata(key string, node Node) error {
	// Text and input bytes are accounted by their independent accumulators.
	node.Payload = Value{}
	if node.Input != nil {
		node.Input = &ToolInput{Kind: node.Input.Kind}
	}
	encoded, err := EncodeValue(node)
	if err != nil {
		return err
	}
	size := len(encoded.Bytes())
	bytes := collector.bytes - collector.metadata[key] + size
	if bytes > collector.limit {
		return streamIssue(LimitExceeded, "/item", "collected item metadata exceeds buffer limit")
	}
	collector.bytes, collector.metadata[key] = bytes, size
	return nil
}

// Finish requires a real terminal and includes usage arriving after completion.
func (collector *ResponseCollector) Finish() (*Response, error) {
	if err := collector.replay.Finish(); err != nil {
		return nil, err
	}
	collector.response.Usage = collector.replay.Usage()
	return &collector.response, nil
}

// Partial returns a snapshot for diagnostics after interruption. It is never a
// successful terminal and cannot authorize execution of incomplete tool calls.
func (collector *ResponseCollector) Partial() (*Response, error) {
	partial := collector.response
	partial.Status = StringValue("incomplete")
	partial.Usage = collector.replay.Usage()
	value, err := EncodeValue(partial)
	if err != nil {
		return nil, err
	}
	var result Response
	if err := value.Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
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
