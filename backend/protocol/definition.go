package protocol

// CompilerVersion binds verification evidence to execution semantics.
const CompilerVersion = "2.0.0-dev.173"

// Preset protocol identifiers. Presets ship with the engine, are read-only for
// authoring, and are refreshed to the shipped revision on every startup;
// customization starts from a copy under a new ID.
const (
	PresetChatCompletionsID = "openai-chat-completions"
	PresetResponsesID       = "openai-responses"
	PresetAnthropicID       = "anthropic-messages"
	PresetGeminiID          = "google-generate-content"
)

// IsPresetProtocolID reports whether the identifier belongs to a built-in
// preset and is therefore exempt from user authoring.
func IsPresetProtocolID(id string) bool {
	switch id {
	case PresetChatCompletionsID, PresetResponsesID, PresetAnthropicID, PresetGeminiID:
		return true
	default:
		return false
	}
}

// Transport identifies framing and connection lifecycle, never content shape.
type Transport string

const (
	HTTPJSON  Transport = "http_json"
	SSE       Transport = "sse"
	NDJSON    Transport = "ndjson"
	WebSocket Transport = "websocket"
)

// NativePolicy controls native replay. A family name alone never authorizes
// cross-account resources, signatures or unregistered semantic capabilities.
type NativePolicy struct {
	Preserve bool `json:"preserve"`
	// ArrayKeys maps a wire array pointer to an item identity pointer. Explicit
	// identities let edits and reordering retain each item's own extensions.
	ArrayKeys map[string]string `json:"arrayKeys,omitempty"`
}

// Operation declares a relative endpoint and independently selected mappings.
// The gateway supplies credentials and authorization outside this definition.
type Operation struct {
	// IsInterleavedThinking is derived from this operation's declared headers by
	// Mapping.headerControls, never inferred from a protocol name by the host.
	IsInterleavedThinking bool              `json:"-"`
	Kind                  string            `json:"kind"`
	Method                string            `json:"method"`
	Path                  string            `json:"path"`
	Transport             Transport         `json:"transport"`
	Request               Direction         `json:"request,omitempty"`
	Response              Direction         `json:"response,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	Query                 map[string]string `json:"query,omitempty"`
	Auth                  Credential        `json:"auth"`
	Framing               *Framing          `json:"framing,omitempty"`
	Task                  *TaskFlow         `json:"task,omitempty"`
	Session               *SessionConfig    `json:"session,omitempty"`
	Models                *ModelDiscovery   `json:"models,omitempty"`
	// Input constrains this operation's wire body independently of the shared
	// decoder, which may also serve inspection or token counting.
	Input *ValueSchema `json:"input,omitempty"`
}

// Credential selects supported credential injection; it contains no secret.
type Credential struct {
	Location string `json:"location"`
	Name     string `json:"name,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// Framing defines end markers and optional SSE event names. JSON event bodies
// still use the same expression compiler as HTTP requests and responses.
type Framing struct {
	// IdleMillis bounds an HTTP stream read, including the initial response.
	// Zero uses DefaultStreamIdleMillis; this is not a total stream lifetime.
	IdleMillis    int      `json:"idleMillis,omitempty"`
	Done          []string `json:"done,omitempty"`
	EventName     string   `json:"eventName,omitempty"`
	EventNamePath string   `json:"eventNamePath,omitempty"`
	BinaryMedia   bool     `json:"binaryMedia,omitempty"`
}

// TaskFlow links separately declared submit/status/result/cancel operations.
// Uncertain submission behavior is owned by the transport, not expressions.
type TaskFlow struct {
	Status            string `json:"status"`
	Result            string `json:"result"`
	Cancel            string `json:"cancel,omitempty"`
	IdempotencyHeader string `json:"idempotencyHeader,omitempty"`
}

// TaskSample asserts status, receipt or control conversion for a linked flow.
// Purpose is submit/status/result/cancel; Kind is decode/encode/control.
type TaskSample struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Kind      string `json:"kind"`
	Purpose   string `json:"purpose"`
	Input     Value  `json:"input"`
	Expected  Value  `json:"expected"`
}

// Sample supplies offline evidence for an explicit direction and capability.
// Expected is required; a successful HTTP status cannot replace an assertion.
type Sample struct {
	ID            string       `json:"id"`
	Operation     string       `json:"operation,omitempty"`
	Direction     Direction    `json:"direction"`
	Capabilities  []Capability `json:"capabilities"`
	Input         Value        `json:"input"`
	Expected      Value        `json:"expected,omitzero"`
	ExpectedIssue IssueCode    `json:"expectedIssue,omitempty"`
	Context       Object       `json:"context,omitempty"`
	Scope         Scope        `json:"scope,omitzero"`
	Sequence      bool         `json:"sequence,omitempty"`
}

// SessionSample replays both independent directions in one ordered trace.
// Direction fixtures alone cannot prove cross-lane tool-result association.
type SessionSample struct {
	ID            string        `json:"id"`
	Operation     string        `json:"operation"`
	Model         Value         `json:"model,omitzero"`
	Steps         []SessionStep `json:"steps"`
	ExpectedIssue IssueCode     `json:"expectedIssue,omitempty"`
	Context       Object        `json:"context,omitempty"`
	Scope         Scope         `json:"scope,omitzero"`
}

// SessionStep asserts the exact result of one declared event direction.
type SessionStep struct {
	Direction Direction `json:"direction"`
	Input     Value     `json:"input"`
	Expected  Value     `json:"expected"`
}

// JSONType describes statically checkable expression and wire field types.
type JSONType string

const (
	AnyType     JSONType = "any"
	ObjectType  JSONType = "object"
	ArrayType   JSONType = "array"
	StringType  JSONType = "string"
	NumberType  JSONType = "number"
	IntegerType JSONType = "integer"
	BooleanType JSONType = "boolean"
	NullType    JSONType = "null"
)

// ValueSchema is the supported schema subset. Unknown schema keywords are
// errors instead of silently ignored validation claims.
type ValueSchema struct {
	Type         JSONType               `json:"type"`
	Required     []string               `json:"required,omitempty"`
	Properties   map[string]ValueSchema `json:"properties,omitempty"`
	Items        *ValueSchema           `json:"items,omitempty"`
	Enum         []Value                `json:"enum,omitempty"`
	AllowUnknown bool                   `json:"allowUnknown,omitempty"`
	Nullable     bool                   `json:"nullable,omitempty"`
}

// Expression is a finite, typed mapping tree. Per-operation field validation
// rejects unrelated keys; loops only traverse a bounded input array.
type Expression struct {
	present       expressionFieldMask
	unknownKeys   []string
	isCompactRead bool
	Op            string                   `json:"op"`
	Operation     string                   `json:"operation,omitempty"`
	Name          string                   `json:"name,omitempty"`
	Namespace     string                   `json:"namespace,omitempty"`
	KeyMode       string                   `json:"keyMode,omitempty"`
	From          string                   `json:"from,omitempty"`
	Path          string                   `json:"path,omitempty"`
	Value         Value                    `json:"value,omitzero"`
	Fields        map[string]Expression    `json:"fields,omitempty"`
	Items         []Expression             `json:"items,omitempty"`
	Source        *Expression              `json:"source,omitempty"`
	Body          *Expression              `json:"body,omitempty"`
	When          *Expression              `json:"when,omitempty"`
	Then          *Expression              `json:"then,omitempty"`
	Otherwise     *Expression              `json:"otherwise,omitempty"`
	Cases         map[string]Expression    `json:"cases,omitempty"`
	Values        map[string]Value         `json:"values,omitempty"`
	To            JSONType                 `json:"to,omitempty"`
	Key           string                   `json:"key,omitempty"`
	Keys          []string                 `json:"keys,omitzero"`
	Prefixes      []string                 `json:"prefixes,omitzero"`
	Policy        string                   `json:"policy,omitempty"`
	OnConflict    *MergeConflictDiagnostic `json:"onConflict,omitempty"`
	OnUnknown     *ObjectFieldDiagnostic   `json:"onUnknown,omitempty"`
	Diagnostic    *RejectDiagnostic        `json:"diagnostic,omitempty"`
	Reason        string                   `json:"reason,omitempty"`
	Ref           string                   `json:"ref,omitempty"`
	Required      bool                     `json:"required,omitempty"`
}
