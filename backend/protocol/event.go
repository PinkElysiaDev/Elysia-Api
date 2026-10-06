package protocol

// EventType enumerates semantic lifecycle operations. Native-only extensions
// use native events and require explicit same-wire compatibility or mappings.
type EventType string

const (
	SessionStarted      EventType = "session.started"
	SessionConfigured   EventType = "session.configured"
	ResponseStarted     EventType = "response.started"
	ItemStarted         EventType = "item.started"
	ItemDelta           EventType = "item.delta"
	ItemSnapshot        EventType = "item.snapshot"
	ItemFinished        EventType = "item.finished"
	UsageUpdated        EventType = "usage.updated"
	ResponseFinished    EventType = "response.finished"
	OperationFailed     EventType = "operation.failed"
	OperationCancelled  EventType = "operation.cancelled"
	MediaReceived       EventType = "media.received"
	NativeEvent         EventType = "native"
	SessionConfigure    EventType = "session.configure"
	InputAppend         EventType = "input.append"
	InputCommit         EventType = "input.commit"
	ResponseCreate      EventType = "response.create"
	ResponseCancel      EventType = "response.cancel"
	ToolResultSubmitted EventType = "tool.result"
	SessionClose        EventType = "session.close"
)

// Media describes an already encoded payload. Reference identifies storage or
// transport-owned bytes; the protocol engine performs no automatic transcoding.
type Media struct {
	Type      string   `json:"type"`
	Format    string   `json:"format"`
	Sequence  Value    `json:"sequence,omitzero"`
	Timestamp Value    `json:"timestamp,omitzero"`
	Reference Resource `json:"reference"`
}

// Event associates deltas and snapshots with stable session/response/item IDs.
type Event struct {
	SchemaVersion int       `json:"schemaVersion"`
	Source        Identity  `json:"source"`
	Type          EventType `json:"type"`
	SessionID     Value     `json:"sessionId,omitzero"`
	ResponseID    Value     `json:"responseId,omitzero"`
	ItemID        Value     `json:"itemId,omitzero"`
	CallID        Value     `json:"callId,omitzero"`
	Sequence      Value     `json:"sequence,omitzero"`
	Index         *int      `json:"index,omitempty"`
	Delta         Value     `json:"delta,omitzero"`
	Item          *Node     `json:"item,omitempty"`
	Response      *Response `json:"response,omitempty"`
	Request       *Request  `json:"request,omitempty"`
	Usage         *Usage    `json:"usage,omitempty"`
	Media         *Media    `json:"media,omitempty"`
	Error         Value     `json:"error,omitzero"`
	Native        *Native   `json:"native,omitempty"`
	// Unmapped is semantic evidence of wire fields without a declared mapping.
	// It may survive same-wire frame replay, but cannot silently cross families.
	Unmapped *Native `json:"unmapped,omitempty"`
}

// TaskStatus includes uncertain submission separately from provider failures.
type TaskStatus string

const (
	TaskSubmitting TaskStatus = "submitting"
	TaskUncertain  TaskStatus = "uncertain"
	TaskQueued     TaskStatus = "queued"
	TaskRunning    TaskStatus = "running"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
	TaskCancelled  TaskStatus = "cancelled"
)

// Task binds an upstream task and settlement identity to an immutable revision.
type Task struct {
	ID            string     `json:"id"`
	Protocol      Identity   `json:"protocol"`
	UpstreamID    Value      `json:"upstreamId,omitzero"`
	ModelSourceID string     `json:"modelSourceId"`
	SettlementID  string     `json:"settlementId"`
	Status        TaskStatus `json:"status"`
	CanCancel     bool       `json:"canCancel"`
	Result        *Response  `json:"result,omitempty"`
}
