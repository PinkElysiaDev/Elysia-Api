package protocol

import "fmt"

// EventOrigin identifies independent session lanes; definitions cannot turn a
// client event into an upstream acknowledgement by changing a type string.
type EventOrigin string

const (
	ClientEvent   EventOrigin = "client"
	UpstreamEvent EventOrigin = "upstream"
)

func isSessionControl(kind EventType) bool {
	switch kind {
	case SessionStarted, SessionConfigured, SessionConfigure, InputAppend, InputCommit, ResponseCreate, ResponseCancel, ToolResultSubmitted, SessionClose:
		return true
	}
	return false
}

// SessionPolicy fixes authorization and automatic-response behavior at handshake.
// An absent Model is for offline traces without a handshake, never a route change.
type SessionPolicy struct {
	Model                    Value
	CanGenerateAutomatically bool
}

type sessionTool struct {
	responseID string
	isReady    bool
	hasResult  bool
}

// SessionReplay validates interleaved requests, responses and client tool
// results. It never executes a tool, reconnects a transport or retains media.
type SessionReplay struct {
	limits                   Limits
	clientTarget             Target
	upstreamTarget           Target
	responses                map[string]*EventReplay
	tools                    map[string]*sessionTool
	clientSequence           *SequenceTracker
	serverSequence           *SequenceTracker
	sessionID                string
	pendingResponses         int
	hasInput                 bool
	isStarted                bool
	isClosed                 bool
	hasFailed                bool
	canGenerateAutomatically bool
	model                    Value
}

// NewSessionReplay constructs one connection's bounded state. Automatic
// server-created responses require an explicit operation declaration (e.g. VAD).
func NewSessionReplay(clientTarget, upstreamTarget Target, limits Limits, policy SessionPolicy) (*SessionReplay, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &SessionReplay{limits: limits, clientTarget: clientTarget, upstreamTarget: upstreamTarget, responses: map[string]*EventReplay{}, tools: map[string]*sessionTool{}, clientSequence: NewSequenceTracker(limits), serverSequence: NewSequenceTracker(limits), canGenerateAutomatically: policy.CanGenerateAutomatically, model: policy.Model}, nil
}

// Consume validates one event before forwarding; false suppresses an identical
// event carrying an explicit sequence number. State belongs to a single loop.
func (session *SessionReplay) Consume(origin EventOrigin, event Event) (bool, error) {
	if origin != ClientEvent && origin != UpstreamEvent {
		return false, streamIssue(InvalidInput, "/origin", "unknown session lane")
	}
	target := session.upstreamTarget
	if origin == ClientEvent {
		target = session.clientTarget
	}
	if err := IssuesError(CheckEvent(event, target, session.limits)); err != nil {
		return false, err
	}

	if !event.SessionID.IsZero() {
		id, err := readString(event.SessionID)
		if err != nil {
			return false, err
		}
		if session.sessionID != "" && id != session.sessionID {
			return false, streamIssue(InvalidAssociation, "/sessionId", "session identity changed")
		}
	}
	accepted, err := session.acceptSequence(origin, event)
	if err != nil || !accepted {
		return accepted, err
	}
	if session.isClosed {
		return false, streamIssue(UpstreamContractViolation, "/type", "event arrived after session close")
	}
	if origin == ClientEvent {
		err = session.consumeClient(event)
	} else {
		err = session.consumeUpstream(event)
	}
	if err != nil {
		return false, err
	}
	return true, session.checkBudget()
}

func (session *SessionReplay) acceptSequence(origin EventOrigin, event Event) (bool, error) {
	tracker := session.serverSequence
	if origin == ClientEvent {
		tracker = session.clientSequence
	}
	return acceptEventSequence(tracker, event)
}

func (session *SessionReplay) consumeClient(event Event) error {
	switch event.Type {
	case SessionConfigure:
		if event.Request == nil {
			return streamIssue(InvalidInput, "/request", "session configuration requires a semantic request")
		}
		if !event.Request.Model.IsZero() {
			if !session.model.IsZero() && !equalValues(session.model, event.Request.Model) {
				return streamIssue(InvalidAssociation, "/request/model", "session configuration cannot change the authorized model")
			}
			session.model = event.Request.Model
		}
	case InputAppend, MediaReceived:
		session.hasInput = true
	case InputCommit:
		if !session.hasInput {
			return streamIssue(InvalidAssociation, "/type", "input commit has no preceding input")
		}
		session.hasInput = false
	case ResponseCreate:
		session.pendingResponses++
	case ResponseCancel:
		id, err := readString(event.ResponseID)
		if err != nil {
			return err
		}
		response, exists := session.responses[id]
		if !exists || response.isTerminal {
			return streamIssue(InvalidAssociation, "/responseId", "cancellation requires an active response")
		}
	case ToolResultSubmitted:
		id, err := readString(event.Item.CallID)
		if err != nil {
			return err
		}
		tool := session.tools[id]
		if tool == nil || !tool.isReady || tool.hasResult {
			return streamIssue(InvalidAssociation, "/item/callId", "tool result requires one completed, unanswered upstream call")
		}
		tool.hasResult = true
	case SessionClose:
		session.isClosed = true
	default:
		return streamIssue(UnsupportedCapability, "/type", "client lane does not permit this upstream event")
	}
	return nil
}

func (session *SessionReplay) consumeUpstream(event Event) error {
	switch event.Type {
	case SessionStarted:
		if session.isStarted {
			return streamIssue(UpstreamContractViolation, "/type", "session started more than once")
		}
		id, err := readString(event.SessionID)
		if err != nil || id == "" {
			return streamIssue(InvalidAssociation, "/sessionId", "session start requires an identity")
		}
		session.sessionID, session.isStarted = id, true
		return nil
	case OperationFailed:
		if event.ResponseID.IsZero() {
			session.isClosed = true
			session.hasFailed = true
			return nil
		}
	case SessionClose:
		if err := session.Finish(); err != nil {
			return err
		}
		session.isClosed = true
		return nil
	case SessionConfigured, MediaReceived:
		if !session.isStarted {
			return streamIssue(InvalidAssociation, "/sessionId", "session event preceded session start")
		}
		return nil
	case SessionConfigure, InputAppend, InputCommit, ResponseCreate, ResponseCancel, ToolResultSubmitted:
		return streamIssue(UnsupportedCapability, "/type", "upstream lane does not permit a client command")
	}
	if !session.isStarted {
		return streamIssue(InvalidAssociation, "/sessionId", "response event preceded session start")
	}
	id, err := readString(event.ResponseID)
	if err != nil || id == "" {
		return streamIssue(InvalidAssociation, "/responseId", "session response events require an identity")
	}
	replay := session.responses[id]
	if event.Type == ResponseStarted {
		if replay != nil {
			return streamIssue(InvalidAssociation, "/responseId", "response identity was reused")
		}
		if session.pendingResponses == 0 && !session.canGenerateAutomatically {
			return streamIssue(UpstreamContractViolation, "/responseId", "unsolicited response requires declared automatic-response support")
		}
		if session.pendingResponses > 0 {
			session.pendingResponses--
		}
		if len(session.responses) >= session.limits.StateItems {
			return streamIssue(LimitExceeded, "/responses", "session response history limit reached")
		}
		replay, err = NewEventReplay(session.upstreamTarget, session.limits)
		if err != nil {
			return err
		}
		session.responses[id] = replay
	}
	if replay == nil {
		return streamIssue(InvalidAssociation, "/responseId", "event has no preceding response start")
	}
	// Sequence belongs to the whole upstream lane, not each interleaved response.
	event.Sequence = Value{}
	if _, err := replay.Consume(event); err != nil {
		return err
	}
	if event.Type == ItemFinished || event.Type == ResponseFinished {
		for _, item := range replay.items {
			if item.kind != ToolCallNode || item.callID == "" {
				continue
			}
			previous := session.tools[item.callID]
			if previous != nil && previous.responseID != id {
				return streamIssue(InvalidAssociation, "/callId", "tool call identity was reused across responses")
			}
			if previous == nil {
				previous = &sessionTool{responseID: id}
				session.tools[item.callID] = previous
			}
			previous.isReady = item.isFinished || event.Type == ResponseFinished
		}
	}
	return nil
}

// SessionResponse records the independently observed outcome of one response.
// An empty terminal means it was interrupted, never implicitly successful.
type SessionResponse struct {
	ID       string
	Terminal EventType
	Usage    *Usage
}

// Responses snapshots each response once after the transport loop has stopped.
func (session *SessionReplay) Responses() []SessionResponse {
	results := make([]SessionResponse, 0, len(session.responses))
	for _, id := range sortedKeys(session.responses) {
		response := session.responses[id]
		results = append(results, SessionResponse{ID: id, Terminal: response.terminal, Usage: response.Usage()})
	}
	return results
}

func (session *SessionReplay) checkBudget() error {
	items := len(session.responses) + len(session.tools) + len(session.clientSequence.records) + len(session.serverSequence.records) + session.pendingResponses
	buffered := 0
	for _, response := range session.responses {
		items += len(response.items) + len(response.identities.aliases) + len(response.sequence.records)
		buffered += response.state.buffered
	}
	if items > session.limits.StateItems || buffered > session.limits.BufferBytes {
		return streamIssue(LimitExceeded, "/session", "aggregate session state exceeds its configured limit")
	}
	return nil
}

// Finish rejects an upstream close with incomplete generation. An explicit
// client session-close command is cancellation, never a fabricated success.
func (session *SessionReplay) Finish() error {
	if session.isClosed {
		return nil
	}
	if !session.isStarted {
		return streamIssue(UpstreamContractViolation, "/sessionId", "connection closed before session start")
	}
	if session.pendingResponses > 0 {
		return streamIssue(UpstreamContractViolation, "/responses", "connection closed with unanswered response requests")
	}
	for id, response := range session.responses {
		if err := response.Finish(); err != nil {
			return fmt.Errorf("response %s: %w", id, err)
		}
	}
	return nil
}

// ResponseUsage returns independent per-response snapshots. Keeping them
// separate avoids turning missing counters in one response into session zeros.
func (session *SessionReplay) ResponseUsage() map[string]*Usage {
	usage := make(map[string]*Usage, len(session.responses))
	for id, response := range session.responses {
		usage[id] = response.Usage()
	}
	return usage
}
