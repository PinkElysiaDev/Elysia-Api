package protocol

import (
	"context"
	"fmt"
)

// ConversionEventState belongs to one upstream attempt. Waiting retains the
// original event order, including interleaved parallel tools, under fixed caps.
// A response terminator closes implicit nodes; an error never flushes partial
// arguments as a successfully completed tool call.
type ConversionEventState struct {
	retention          streamReservation
	conversion         *CompiledConversion
	route              ConversionContext
	pending            []Event
	open               map[string]bool
	bytes              int
	openBytes          int
	Buffered           bool
	isReleased         bool
	hasResponseStarted bool
}

func NewConversionEventState(c *CompiledConversion, route ConversionContext) *ConversionEventState {
	if route.Delivery == nil {
		route.Delivery = NewDeliveryState()
	}
	return &ConversionEventState{conversion: c, route: route, open: map[string]bool{}}
}

func (s *ConversionEventState) Push(event Event) ([]Event, error) {
	return s.PushContext(context.Background(), event)
}

// PushContext preserves source order while waiting only for declared resources
// that need a completed, authenticated entity.
func (s *ConversionEventState) PushContext(ctx context.Context, event Event) (events []Event, err error) {
	if s.isReleased {
		return nil, streamIssue(InvalidAssociation, "/state", "released conversion events cannot resume")
	}
	hasResponseStarted := s.hasResponseStarted
	defer func() {
		hasGeneratedPrelude := false
		if err == nil && len(events) != 0 && s.conversion != nil && len(s.conversion.preludes) != 0 && !hasResponseStarted {
			count := len(events)
			events, hasResponseStarted, err = s.conversion.prependResponse(ctx, events, s.route)
			hasGeneratedPrelude = len(events) > count
		}
		if err == nil && s.retention.budget != nil {
			err = s.commitRetention()
			if err != nil {
				events = nil
			}
		}
		if err == nil {
			s.hasResponseStarted = hasResponseStarted
			s.Buffered = s.Buffered || hasGeneratedPrelude
		}
	}()
	if s.conversion == nil {
		return []Event{event}, nil
	}
	if event.Type == OperationFailed || event.Type == OperationCancelled {
		// A real failure ends waiting without publishing partial buffered items.
		clear(s.open)
		s.openBytes = 0
		s.pending, s.bytes = nil, 0
		return []Event{event}, nil
	}
	wait := false
	if event.Type == ItemStarted && event.Item != nil {
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
		s.setOpen(key, true)
		s.Buffered = true
	}
	if entity := event.continuation; entity != nil {
		key := "continuation:" + entity.key
		if entity.isComplete {
			s.setOpen(key, false)
		} else if event.Item != nil && HasContinuationState(*event.Item) {
			needsRecovery, err := s.conversion.NeedsContinuationRecovery(ctx, ConversionEvent, *event.Item, s.route)
			if err != nil {
				return nil, err
			}
			if needsRecovery && !s.open[key] {
				if len(s.open) >= s.conversion.limits.StateItems {
					return nil, streamIssue(LimitExceeded, "/conversion/events", "waiting node limit exceeded")
				}
				s.setOpen(key, true)
				s.Buffered = true
			}
		}
	}
	if len(s.pending) == 0 && len(s.open) == 0 {
		return []Event{event}, nil
	}
	raw, err := EncodeValue(event)
	if err != nil {
		return nil, err
	}
	if len(s.pending) >= s.conversion.limits.Nodes || s.bytes+len(raw.Bytes()) > semanticLimits(s.conversion.limits).BufferBytes {
		return nil, streamIssue(LimitExceeded, "/conversion/events", "waiting event buffer exceeded")
	}
	s.bytes += len(raw.Bytes())
	s.pending = append(s.pending, event)
	if event.Type == ItemFinished {
		s.setOpen(key, false)
	}
	if event.Type == ResponseFinished {
		clear(s.open)
		s.openBytes = 0
	}
	if len(s.open) > 0 {
		return nil, nil
	}
	out := s.pending
	s.pending = nil
	s.bytes = 0
	return out, nil
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
			response := Response{SchemaVersion: copy[i].SchemaVersion}
			if copy[i].Response != nil {
				response = *copy[i].Response
			}
			response.Usage = MergeUsage(response.Usage, counts)
			copy[i].Response = &response
		}
	}
	return copy
}

// Drain checks that every explicitly buffered node has completed.
// Wire terminal buffering belongs to the target Mapping's stream state.
func (s *ConversionEventState) Drain() ([]Event, error) {
	return nil, s.Finish()
}

func (s *ConversionEventState) Finish() error {
	if s.isReleased {
		return streamIssue(InvalidAssociation, "/state", "released conversion events cannot finish")
	}
	if budget := s.retention.budget; budget != nil {
		if err := budget.Check(); err != nil {
			return err
		}
	}
	if len(s.pending) > 0 || len(s.open) > 0 {
		return streamIssue(InvalidAssociation, "/conversion/events", "stream ended with incomplete waiting nodes")
	}
	return nil
}

// Release discards waiting events without publishing them as completed input.
func (s *ConversionEventState) Release() {
	s.retention.release()
	*s = ConversionEventState{isReleased: true}
}

func ContinuationResourceKey(resource Resource) string {
	value, _ := EncodeValue(resource)
	return "continuation-resource:" + hashValue(value)
}
