package protocol

import "strings"

type collectedPosition struct {
	continuation *continuationEntity
	parent       string
	collection   string
	index        int
}

// ResponseCollector assembles a bounded result for consumers that need a whole
// response (Agent and probes). Ordinary gateway streams use EventReplay alone.
type ResponseCollector struct {
	retention streamReservation
	replay    *EventReplay
	response  Response
	items     map[string]collectedPosition
	// Auxiliary native frames cannot establish an identity or borrow one from
	// an unrelated later event; bind them to the observed response start.
	startedResponseID Value
	// Prefix entries become immutable once preceding messages close. Advancing
	// this frontier visits each top-level item once across the whole stream.
	topKeys                      []string
	ordinals                     []int
	containers                   bool
	hasServerTools               bool
	texts                        map[string]*collectedText
	metadata                     map[string]int
	metadataDeltas               map[string][]*collectedMetadata
	metadataItems, metadataBytes int
	unmapped                     []Value
	unmappedBytes                int
	headerBaseBytes              int
	headerBytes, snapshotItems   int
	proofBytes                   int
	usageBytes, usageItems       int
	failure                      error
	hasResult                    bool
	isReleased                   bool
	bytes                        int
	limit                        int
}

type collectedText struct {
	strings.Builder
	dirty bool
}

// Text is encoded only at an observation boundary, never after every delta.
// Replay still validates every delta/snapshot before it reaches this owner.
func (collector *ResponseCollector) materializeText(key string) {
	text := collector.texts[key]
	if text != nil && text.dirty {
		item := collector.item(key)
		if item.Kind == ServerToolNode {
			item.Input.Value = StringValue(text.String())
		} else {
			item.Payload = StringValue(text.String())
		}
		text.dirty = false
	}
}

func (collector *ResponseCollector) materializeTexts() {
	for key := range collector.texts {
		collector.materializeText(key)
	}
}

// CompletedNode returns an isolated snapshot only at an acknowledged item end.
// Retaining its continuation proof can fail the shared budget; that failure is
// distinct from a node whose final ordinal is not yet known.
func (collector *ResponseCollector) CompletedNode(event Event) (Node, int, bool, error) {
	if collector.failure != nil {
		return Node{}, 0, false, collector.failure
	}
	if event.Type != ItemFinished {
		return Node{}, 0, false, nil
	}
	key, err := collector.replay.itemKey(event)
	if err != nil {
		return Node{}, 0, false, nil
	}
	position, ok := collector.items[key]
	if !ok || position.collection != "" {
		return Node{}, 0, false, nil
	}
	node := collector.item(key)
	if node.Kind == MessageNode {
		return Node{}, 0, false, nil
	}
	top := position.index
	if position.parent != "" {
		top = collector.items[position.parent].index
	}
	// A preceding open message could still append children and shift this
	// ordinal. Such nodes are captured from the complete terminal response.
	if len(collector.ordinals) == 0 {
		collector.ordinals = append(collector.ordinals, 0)
	}
	for len(collector.ordinals) <= top {
		index := len(collector.ordinals) - 1
		previous := collector.replay.items[collector.topKeys[index]]
		if previous != nil && previous.kind == MessageNode && !previous.isFinished {
			return Node{}, 0, false, nil
		}
		n := collector.response.Content[index]
		count := 1
		if n.Kind == MessageNode {
			count = len(n.Children)
		}
		collector.ordinals = append(collector.ordinals, collector.ordinals[index]+count)
	}
	ordinal := collector.ordinals[top]
	if position.parent != "" {
		ordinal += position.index
	}
	collector.materializeText(key)
	collector.materializeItemMetadata(key)
	if position.continuation != nil {
		if err := collector.completeContinuation(position.continuation, *node); err != nil {
			return Node{}, 0, false, err
		}
	}
	if node.Kind == ServerToolNode {
		return cloneServerTool(node), ordinal, true, nil
	}
	return CanonicalReasoning(cloneNodes([]Node{*node})[0]), ordinal, true, nil
}

func (collector *ResponseCollector) item(key string) *Node {
	p := collector.items[key]
	if p.parent != "" {
		parent := collector.item(p.parent)
		if p.collection == "reasoning_content" {
			return &parent.ReasoningContent[p.index]
		}
		return &parent.Children[p.index]
	}
	return &collector.response.Content[p.index]
}

// NewResponseCollector uses the same lifecycle validator as live forwarding.
func NewResponseCollector(target Target, limits Limits) (*ResponseCollector, error) {
	replay, err := NewEventReplay(target, limits)
	if err != nil {
		return nil, err
	}
	response := Response{SchemaVersion: target.coreSchemaVersion(), Source: target.Protocol}
	base, err := measureSemanticValue(&response, semanticLimits(limits))
	if err != nil {
		return nil, err
	}
	return &ResponseCollector{replay: replay, response: response, items: map[string]collectedPosition{}, texts: map[string]*collectedText{}, metadata: map[string]int{}, limit: limits.BufferBytes, headerBaseBytes: base.bytes}, nil
}

// Consume returns the newly appended text, if any. Snapshot prefixes and
// duplicate sequence frames never appear twice in a caller's output.
func (collector *ResponseCollector) Consume(event Event) (kind NodeKind, delta string, err error) {
	if collector.isReleased {
		return "", "", streamIssue(InvalidAssociation, "/state", "released response collector cannot resume")
	}
	if collector.failure != nil {
		return "", "", collector.failure
	}
	// Public callers can bypass frame decoding. Bound the auxiliary frame
	// before sequence canonicalization or metadata validation copies its body.
	if event.Type == NativeEvent && event.Native != nil && event.Native.Source.Path == responsesMetadataPath {
		if err := checkResourceValue(event.Native.Value, collector.replay.limits, "wire_input"); err != nil {
			return "", "", err
		}
	}
	isAccepted, err := collector.replay.Consume(event)
	if err != nil || !isAccepted {
		return "", "", err
	}
	defer func() {
		if err == nil {
			err = collector.checkRetention()
			if err != nil {
				kind, delta = "", ""
			}
		}
		if err != nil {
			collector.failure = err
			collector.materializeTexts()
			collector.materializeMetadata()
			clear(collector.texts)
			collector.replay.releaseToolInputs()
			collector.replay.retention.commit(collector.replay.retainedItems(), collector.replay.retainedBytes())
		}
	}()
	if event.Unmapped != nil {
		if err := collector.collectUnmapped(event.Unmapped); err != nil {
			return "", "", err
		}
	}
	if event.controlReceipt != nil {
		return "", "", nil
	}
	if event.auxiliary != nil {
		if collector.startedResponseID.IsZero() || !equalValues(collector.startedResponseID, event.ResponseID) {
			return "", "", streamIssue(InvalidAssociation, "/responseId", "auxiliary event requires the identity of an earlier response start")
		}
		return "", "", nil
	}
	if isMetadata, err := isResponsesMetadataEvent(event); isMetadata || err != nil {
		if err == nil && (collector.startedResponseID.IsZero() || !equalValues(collector.startedResponseID, event.ResponseID)) {
			err = streamIssue(InvalidAssociation, "/response_id", "auxiliary metadata requires the identity of an earlier response start")
		}
		return "", "", err
	}
	header := collector.response
	metadata, hasMetadata := mergeCollectedMetadata(header.Metadata, event.Metadata)
	hasIdentity := !event.ResponseID.IsZero() && event.ResponseID != header.ID
	if hasMetadata || hasIdentity {
		header.Metadata = metadata
		if hasIdentity {
			header.ID = event.ResponseID
		}
		if err := collector.setResponse(header, false); err != nil {
			return "", "", err
		}
	}
	if event.Response != nil {
		if err := CheckGenerationOutcome(event.Response); err != nil {
			return "", "", err
		}
	}
	switch event.Type {
	case ResponseStarted:
		collector.startedResponseID = event.ResponseID
		if event.Response != nil {
			collector.materializeMetadata()
			id, metadata := collector.response.ID, collector.response.Metadata
			snapshot := *event.Response
			if snapshot.ID.IsZero() {
				snapshot.ID = id
			}
			snapshot.Metadata, _ = mergeCollectedMetadata(metadata, event.Response.Metadata)
			if err := collector.setResponse(snapshot, true); err != nil {
				return "", "", err
			}
			collector.ownServerResponseContent()
			if !event.Response.ID.IsZero() {
				collector.startedResponseID = event.Response.ID
			}
		}
		return "", "", nil
	case NativeEvent, MediaReceived:
		return "", "", streamIssue(UnsupportedCapability, "/type", "bounded response collection cannot represent native or realtime media events")
	case OperationFailed, OperationCancelled:
		return "", "", streamIssue(UpstreamContractViolation, "/type", "generation ended with "+string(event.Type))
	case ResponseFinished:
		collector.materializeTexts()
		collector.materializeMetadata()
		if err := collector.finishTools(); err != nil {
			return "", "", err
		}
		if event.Response != nil {
			if len(event.Response.Content) > 0 && len(collector.items) > 0 {
				compare := collectedOutput
				if collector.containers {
					compare = collectedContainers
				}
				actual, err := EncodeValue(compare(collector.response.Content))
				if err != nil {
					return "", "", err
				}
				expected, err := EncodeValue(compare(event.Response.Content))
				if err != nil {
					return "", "", err
				}
				if !equalValues(actual, expected) {
					return "", "", streamIssue(UpstreamContractViolation, "/response/content", "terminal content differs from streamed items")
				}
			}
			content, id, model := collector.response.Content, collector.response.ID, collector.response.Model
			attributes, metadata := collector.response.Attributes, collector.response.Metadata
			snapshot := *event.Response
			snapshot.Attributes = copyObject(attributes)
			for key, value := range event.Response.Attributes {
				snapshot.Attributes[key] = value
			}
			snapshot.Metadata, _ = mergeCollectedMetadata(metadata, event.Response.Metadata)
			if snapshot.ID.IsZero() {
				snapshot.ID = id
			}
			if snapshot.Model.IsZero() {
				snapshot.Model = model
			}
			if len(snapshot.Content) == 0 {
				snapshot.Content = content
			}
			if err := collector.setResponse(snapshot, true); err != nil {
				return "", "", err
			}
			collector.ownServerResponseContent()
		}
		clear(collector.texts)
		return "", "", nil
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		return collector.collectItem(event)
	default:
		return "", "", nil
	}
}

// collectUnmapped keeps wire fields without a declared mapping instead of
// discarding them: relays attach billing and vendor metadata to stream frames,
// and same-wire replay preserves those for gateway clients. Collected
// responses carry the same evidence under Attributes["wire:stream"], bounded
// by the shared buffer limit.
func (collector *ResponseCollector) collectUnmapped(native *Native) error {
	size := native.Value.Len()
	if err := collector.checkRetainedBytes(collector.bytes + size); err != nil {
		return err
	}
	if err := collector.reserveItems(1); err != nil {
		return err
	}
	collector.bytes += size
	collector.unmappedBytes += size
	collector.unmapped = append(collector.unmapped, native.Value)
	return nil
}

func (collector *ResponseCollector) attachUnmapped(response *Response) {
	if len(collector.unmapped) == 0 {
		return
	}
	value, err := EncodeValue(collector.unmapped)
	if err != nil {
		return
	}
	if response.Attributes == nil {
		response.Attributes = Object{}
	}
	response.Attributes["wire:stream"] = value
}

// A terminal may wrap streamed leaves in output messages. Compare generated
// payloads here; the returned terminal still retains its complete identities
// and native attributes. Wire fidelity is verified separately by frame replay.
func collectedOutput(nodes []Node) []Node {
	var output []Node
	for _, node := range nodes {
		node = CanonicalReasoning(node)
		if node.Kind == MessageNode {
			if len(node.Children) > 0 {
				children := cloneNodes(node.Children)
				for i := range children {
					children[i].Metadata = MergeNodeMetadata(children[i].Metadata, node.Metadata, false)
				}
				output = append(output, collectedOutput(children)...)
				continue
			}
		}
		node.Native = nil
		node.Source = nil
		node.Metadata = comparableMetadata(node.Metadata)
		if node.Kind != ServerToolNode {
			node.ID, node.Status = Value{}, Value{}
		}
		node.Children = comparableNodes(node.Children)
		if node.ReasoningContent != nil {
			node.ReasoningContent = comparableNodes(node.ReasoningContent)
			if node.ReasoningContent == nil {
				node.ReasoningContent = []Node{}
			}
		}
		output = append(output, node)
	}
	return output
}

func (collector *ResponseCollector) collectItem(event Event) (NodeKind, string, error) {
	key, err := collector.replay.itemKey(event)
	if err != nil {
		return "", "", err
	}
	if collector.replay.items[key].kind == ServerToolNode {
		return collector.collectServerTool(key, event)
	}
	if event.Type == ItemStarted {
		node := *event.Item
		node.Children = cloneNodes(node.Children)
		node.ReasoningContent = cloneNodes(node.ReasoningContent)
		collector.registerItem(key, event, node)
		collector.texts[key] = &collectedText{}
	}
	item := collector.item(key)
	isDeferred, err := collector.deferMetadataDelta(key, event)
	if err != nil {
		return "", "", err
	}
	if !isDeferred {
		collector.materializeItemMetadata(key)
	}
	if event.Item != nil {
		item.Metadata = MergeNodeMetadata(item.Metadata, event.Item.Metadata, false)
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
		if event.Type != ItemStarted && event.Item.Children != nil && item.Kind != MessageNode && !collector.replay.items[key].hasSummaryChildren {
			item.Children = append([]Node(nil), event.Item.Children...)
		}
		if event.Type != ItemStarted && event.Item.ReasoningContent != nil && !collector.replay.items[key].hasContentChildren {
			item.ReasoningContent = cloneNodes(event.Item.ReasoningContent)
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
	if !isDeferred {
		if err := collector.mergeEventMetadata(key, event); err != nil {
			return "", "", err
		}
	}
	if !event.CallID.IsZero() {
		item.CallID = event.CallID
	}
	if event.Item != nil || !event.CallID.IsZero() || (!isDeferred && len(event.Metadata) != 0) {
		if err := collector.accountItemMetadata(key, *item); err != nil {
			return "", "", err
		}
	}
	value := event.Delta
	isSnapshot := IsItemSnapshot(event.Type)
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
		} else {
			delta, err = readString(value)
			if err != nil {
				return "", "", err
			}
		}
		if isSnapshot {
			if item.Kind == ToolCallNode && item.Input.Kind == JSONInput {
				delta, err = JSONToolSnapshotSuffix(buffer.String(), delta)
				if err != nil {
					return "", "", err
				}
			} else {
				delta = delta[buffer.Len():]
			}
		} // Replay already verified the prefix.
		if err := collector.checkRetainedBytes(collector.bytes + len(delta)); err != nil {
			return "", "", err
		}
		buffer.WriteString(delta)
		collector.bytes += len(delta)
	}
	if item.Kind != ToolCallNode {
		if item.ReasoningForm != "" || item.Kind == MessageNode {
			return item.Kind, "", nil
		}
		for _, resource := range item.Resources {
			if resource.Kind == "encrypted_content" && item.Payload.IsZero() && !buffer.dirty {
				return item.Kind, "", nil
			}
		}
		buffer.dirty = true
		if event.Collection != "" {
			if event.Type == ItemFinished {
				collector.materializeText(key)
				collector.materializeItemMetadata(key)
			}
			return ReasoningNode, delta, nil
		}
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

func (collector *ResponseCollector) registerItem(key string, event Event, node Node) {
	parent := collector.replay.items[key].parent
	if parent == "" {
		collector.items[key] = collectedPosition{index: len(collector.response.Content)}
		for len(collector.topKeys) < len(collector.response.Content) {
			collector.topKeys = append(collector.topKeys, "")
		}
		collector.topKeys = append(collector.topKeys, key)
		collector.response.Content = append(collector.response.Content, node)
	} else {
		parentNode := collector.item(parent)
		children := &parentNode.Children
		if event.Collection == "reasoning_content" {
			children = &parentNode.ReasoningContent
		}
		collector.items[key] = collectedPosition{parent: parent, collection: event.Collection, index: len(*children)}
		*children = append(*children, node)
	}
	collector.containers = collector.containers || node.Kind == MessageNode
}

func (collector *ResponseCollector) accountItemMetadata(key string, node Node) error {
	// Text and input bytes are accounted by their independent accumulators.
	node.Payload = Value{}
	item := collector.replay.items[key]
	if node.Kind == MessageNode || item.hasSummaryChildren {
		node.Children = nil // Associated children have independent budgets.
	}
	if item.hasContentChildren {
		node.ReasoningContent = nil
	}
	if node.Input != nil {
		node.Input = &ToolInput{Kind: node.Input.Kind}
	}
	encoded, err := EncodeValue(node)
	if err != nil {
		return err
	}
	size := len(encoded.Bytes())
	bytes := collector.bytes - collector.metadata[key] + size
	if err := collector.checkRetainedBytes(bytes); err != nil {
		return err
	}
	collector.bytes, collector.metadata[key] = bytes, size
	return nil
}

// Finish requires a real terminal and includes usage arriving after completion.
func (collector *ResponseCollector) Finish() (*Response, error) {
	if collector.isReleased {
		return nil, streamIssue(InvalidAssociation, "/state", "released response collector cannot finish")
	}
	if collector.failure != nil {
		return nil, collector.failure
	}
	if err := collector.replay.Finish(); err != nil {
		return nil, err
	}
	collector.materializeTexts()
	collector.materializeMetadata()
	if err := collector.collectUsage(); err != nil {
		collector.failure = err
		return nil, err
	}
	for i := range collector.response.Content {
		collector.response.Content[i] = CanonicalReasoning(collector.response.Content[i])
	}
	collector.attachUnmapped(&collector.response)
	if _, err := measureSemanticValue(&collector.response, collector.replay.limits); err != nil {
		return nil, err
	}
	collector.hasResult = true
	return &collector.response, nil
}

// Partial returns a snapshot for diagnostics after interruption. It is never a
// successful terminal and cannot authorize execution of incomplete tool calls.
func (collector *ResponseCollector) Partial() (*Response, error) {
	if collector.isReleased {
		return nil, streamIssue(InvalidAssociation, "/state", "released response collector cannot produce a snapshot")
	}
	collector.materializeTexts()
	collector.materializeMetadata()
	partial := collector.response
	partial.Status = StringValue("incomplete")
	partial.Usage = collector.replay.Usage()
	collector.attachUnmapped(&partial)
	value, err := EncodeValue(partial)
	if err != nil {
		return nil, err
	}
	if err := checkResourceValue(value, collector.replay.limits, "collect.partial"); err != nil {
		return nil, err
	}
	var result Response
	if err := value.Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Release drops accumulation and validation state after its consumer settles.
// A successful Finish result keeps its response storage; Partial owns a copy.
func (collector *ResponseCollector) Release() {
	collector.retention.release()
	collector.replay.Release()
	var response Response
	if collector.hasResult {
		response = collector.response
	}
	*collector = ResponseCollector{response: response, replay: collector.replay, hasResult: collector.hasResult, isReleased: true}
}

func (collector *ResponseCollector) finishTools() error {
	for key := range collector.items {
		item := collector.item(key)
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

// IsItemSnapshot 判定事件是否携带全量快照（非增量 delta）。
func IsItemSnapshot(eventType EventType) bool { return eventType != ItemDelta }

func mergeCollectedMetadata(left, right []ResponseMetadata) ([]ResponseMetadata, bool) {
	out := left
	hasChanged := false
	for _, m := range right {
		if m.Location != "response" && m.Location != "usage" {
			continue
		}
		position := metadataPosition(out, m)
		if position >= 0 && out[position] == m {
			continue
		}
		if !hasChanged {
			out = append([]ResponseMetadata(nil), left...)
			hasChanged = true
		}
		if position < 0 {
			out = append(out, m)
		} else {
			out[position] = m
		}
	}
	return out, hasChanged
}

// Node metadata snapshots replace earlier values. Token probabilities arriving
// as deltas append; response, usage and transport event metadata never migrates
// to a content item. Event padding belongs to frame replay, not chat history.
func MergeNodeMetadata(left, right []ResponseMetadata, delta bool) []ResponseMetadata {
	out := append([]ResponseMetadata(nil), left...)
	for _, m := range right {
		if m.Location == "response" || m.Location == "usage" || strings.HasPrefix(m.Location, "event:") {
			continue
		}
		found := false
		for i := range out {
			if out[i].Codec == m.Codec && out[i].Location == m.Location && out[i].Name == m.Name {
				if delta && m.Name == "logprobs" {
					m.Value = appendLogprobValues(out[i].Value, m.Value, m.Codec)
				}
				out[i], found = m, true
				break
			}
		}
		if !found {
			out = append(out, m)
		}
	}
	return out
}

func appendLogprobValues(a, b Value, codec string) Value {
	if codec == "openai-chat" {
		left, _ := a.ReadObject()
		right, _ := b.ReadObject()
		if left == nil {
			left = Object{}
		}
		for k, v := range right {
			left[k] = appendLogprobValues(left[k], v, "responses")
		}
		out, _ := EncodeValue(left)
		return out
	}
	var left, right []Value
	if a.Decode(&left) != nil || b.Decode(&right) != nil {
		return b
	}
	out, _ := EncodeValue(append(left, right...))
	return out
}
