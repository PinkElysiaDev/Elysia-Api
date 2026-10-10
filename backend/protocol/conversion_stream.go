package protocol

import "fmt"

// ConversionEventState belongs to one upstream attempt. Waiting retains the
// original event order, including interleaved parallel tools, under fixed caps.
// A response terminator closes implicit nodes; an error never flushes partial
// arguments as a successfully completed tool call.
type ConversionEventState struct {
	conversion *CompiledConversion
	route      ConversionContext
	pending    []Event
	open       map[string]bool
	bytes      int
	Buffered   bool
	usage      *Usage
	terminal   *Event
	id         Value
	model      Value
	started    bool
}

func NewConversionEventState(c *CompiledConversion, route ConversionContext) *ConversionEventState {
	if route.Delivery == nil {
		route.Delivery = NewDeliveryState()
	}
	return &ConversionEventState{conversion: c, route: route, open: map[string]bool{}}
}

func (s *ConversionEventState) Push(event Event) ([]Event, error) {
	if s.conversion == nil {
		return []Event{event}, nil
	}
	if s.conversion.hasDeliveryRule(s.route) && !s.started && event.Type != ResponseStarted && (event.Type == ItemStarted || event.Type == ResponseFinished) {
		start, err := s.Push(Event{SchemaVersion: event.SchemaVersion, Source: event.Source, Type: ResponseStarted, ResponseID: event.ResponseID})
		if err != nil {
			return nil, err
		}
		s.Buffered = true
		rest, err := s.Push(event)
		return append(start, rest...), err
	}
	if event.Type == ResponseStarted && s.conversion.hasDeliveryRule(s.route) {
		s.started = true
		if event.Response == nil {
			event.Response = &Response{SchemaVersion: SemanticSchemaVersion, ID: event.ResponseID}
		} else {
			copy := *event.Response
			copy.Attributes = copyObject(event.Response.Attributes)
			event.Response = &copy
		}
		if event.Response.ID.IsZero() {
			event.Response.ID = event.ResponseID
		}
		prepareDelivery(event.Response, s.route, s.conversion.deliveryCodec(s.route))
		s.id, s.model = event.Response.ID, event.Response.Model
		s.route.Delivery.ID = s.id
		if created := event.Response.Attributes["created_at"]; !created.IsZero() {
			s.route.Delivery.Created = created
		}
	}
	if !s.id.IsZero() && s.conversion.hasDeliveryRule(s.route) {
		event.ResponseID = s.id
		if event.Response != nil {
			copy := *event.Response
			copy.Attributes = copyObject(event.Response.Attributes)
			copy.ID = s.id
			copy.Model = s.model
			if copy.Attributes == nil {
				copy.Attributes = Object{}
			}
			if codec := s.conversion.deliveryCodec(s.route); (codec == "openai-chat" || codec == "responses") && !s.route.Delivery.Created.IsZero() {
				copy.Attributes["created_at"] = s.route.Delivery.Created
			}
			event.Response = &copy
		}
	}
	if s.conversion.HasAnthropicEnvelope(ConversionEvent, s.route) {
		s.usage = MergeUsage(s.usage, event.Usage)
		if event.Response != nil {
			s.usage = MergeUsage(s.usage, event.Response.Usage)
		}
		if event.Type == ResponseStarted {
			if event.Response == nil {
				event.Response = &Response{SchemaVersion: SemanticSchemaVersion, ID: event.ResponseID}
			} else {
				copy := *event.Response
				event.Response = &copy
			}
			if event.Response.ID.IsZero() {
				event.Response.ID = event.ResponseID
			}
			ensureDeliveryIdentity(event.Response, s.route)
			s.id, s.model = event.Response.ID, event.Response.Model
			event.ResponseID = s.id
			event.Response.Usage = MergeUsage(nil, s.usage)
		}
		if !s.id.IsZero() {
			event.ResponseID = s.id
			if event.Response != nil {
				copy := *event.Response
				copy.ID, copy.Model = s.id, s.model
				event.Response = &copy
			}
		}
		if event.Type == ResponseFinished {
			if s.terminal != nil {
				return nil, streamIssue(InvalidAssociation, "/event", "duplicate terminal")
			}
			copy := event
			s.terminal = &copy
			s.Buffered = true
			// Close implicit items but retain terminal until usage tail frames arrive.
			clear(s.open)
			out := s.pending
			s.pending, s.bytes = nil, 0
			return out, nil
		}
		if event.Type == OperationFailed || event.Type == OperationCancelled {
			s.terminal = nil
		}
	}
	wait := false
	if event.Type == ItemStarted && event.Item != nil {
		wait = s.conversion.Policy.Mode == "strict" && s.conversion.SignatureProjectionEnabled(ConversionEvent, s.route) && (s.route.Source.Family != s.route.Target.Family || s.route.Source.WireVersion != s.route.Target.WireVersion)
		value, _ := EncodeValue(event.Item)
		for _, r := range s.conversion.Policy.Rules {
			if r.Enabled && r.Phase == ConversionEvent && r.Action == "buffer_node" && (r.Match.NodeKind == "" || r.Match.NodeKind == event.Item.Kind) && r.Match.matches(s.route, value) {
				wait = true
			}
		}
	}
	key := string(event.ItemID.Bytes())
	if key == "" && event.Index != nil {
		key = fmt.Sprintf("index:%d", *event.Index)
	}
	if wait {
		if len(s.open) >= s.conversion.limits.StateItems {
			return nil, streamIssue(LimitExceeded, "/conversion/events", "waiting node limit exceeded")
		}
		s.open[key] = true
		s.Buffered = true
	}
	if len(s.pending) == 0 && len(s.open) == 0 {
		return []Event{event}, nil
	}
	raw, err := EncodeValue(event)
	if err != nil {
		return nil, err
	}
	if len(s.pending) >= s.conversion.limits.Nodes || s.bytes+len(raw.Bytes()) > s.conversion.limits.BufferBytes {
		return nil, streamIssue(LimitExceeded, "/conversion/events", "waiting event buffer exceeded")
	}
	s.bytes += len(raw.Bytes())
	s.pending = append(s.pending, event)
	if event.Type == ItemFinished {
		delete(s.open, key)
	}
	if event.Type == ResponseFinished {
		clear(s.open)
	}
	if len(s.open) > 0 {
		return nil, nil
	}
	out := s.pending
	s.pending = nil
	s.bytes = 0
	return out, nil
}

// Drain finalizes client projection after the provider's usage-only tail.
func (s *ConversionEventState) Drain() ([]Event, error) {
	if err := s.Finish(); err != nil {
		return nil, err
	}
	if s.terminal == nil {
		return nil, nil
	}
	event := *s.terminal
	s.terminal = nil
	if event.Response == nil {
		event.Response = &Response{SchemaVersion: SemanticSchemaVersion}
	} else {
		copy := *event.Response
		event.Response = &copy
	}
	event.Response.Usage = MergeUsage(nil, s.usage)
	event.Response.ID, event.Response.Model, event.ResponseID = s.id, s.model, s.id
	return []Event{event}, nil
}

// DeliveryFrameEvents makes same-frame usage available before message_start.
// Provider event order and provider accounting remain unchanged.
func DeliveryFrameEvents(events []Event) []Event {
	usage := map[Value]*Usage{}
	identities := map[Value]bool{}
	identity := func(event Event) Value {
		if !event.ResponseID.IsZero() && !event.ResponseID.IsNull() {
			return event.ResponseID
		}
		if event.Response != nil && !event.Response.ID.IsNull() {
			return event.Response.ID
		}
		return Value{}
	}
	for _, event := range events {
		id := identity(event)
		if !id.IsZero() {
			identities[id] = true
		}
		usage[id] = MergeUsage(usage[id], event.Usage)
		if event.Response != nil {
			usage[id] = MergeUsage(usage[id], event.Response.Usage)
		}
	}
	copy := append([]Event(nil), events...)
	for i := range copy {
		if copy[i].Type == ResponseStarted {
			id := identity(copy[i])
			counts := usage[id]
			if !id.IsZero() && len(identities) == 1 {
				counts = MergeUsage(counts, usage[Value{}])
			}
			if counts == nil {
				continue
			}
			response := Response{SchemaVersion: SemanticSchemaVersion}
			if copy[i].Response != nil {
				response = *copy[i].Response
			}
			response.Usage = MergeUsage(response.Usage, counts)
			copy[i].Response = &response
		}
	}
	return copy
}

func (s *ConversionEventState) Finish() error {
	if len(s.pending) > 0 || len(s.open) > 0 {
		return streamIssue(InvalidAssociation, "/conversion/events", "stream ended with incomplete waiting nodes")
	}
	return nil
}

func ContinuationResourceKey(resource Resource) string {
	value, _ := EncodeValue(resource)
	return "continuation-resource:" + hashValue(value)
}
