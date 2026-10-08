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
}

func NewConversionEventState(c *CompiledConversion, route ConversionContext) *ConversionEventState {
	return &ConversionEventState{conversion: c, route: route, open: map[string]bool{}}
}

func (s *ConversionEventState) Push(event Event) ([]Event, error) {
	if s.conversion == nil {
		return []Event{event}, nil
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

func (s *ConversionEventState) Finish() error {
	if len(s.pending) > 0 || len(s.open) > 0 {
		return streamIssue(InvalidAssociation, "/conversion/events", "stream ended with incomplete waiting nodes")
	}
	return nil
}

func SignatureRecoveryKey(resource Resource) string { return "signature:" + hashValue(resource.ID) }
