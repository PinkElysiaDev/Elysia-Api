package protocol

// DefinitionSchemaVersion versions the configuration language independently
// from both provider revisions and the semantic model.
const DefinitionSchemaVersion = 2

// CompilerVersion binds verification evidence to execution semantics.
const CompilerVersion = "2.0.0-dev.5"

// Transport identifies framing and connection lifecycle, never content shape.
type Transport string

const (
	HTTPJSON  Transport = "http_json"
	SSE       Transport = "sse"
	NDJSON    Transport = "ndjson"
	WebSocket Transport = "websocket"
)

// Definition is a strictly decoded, versioned gateway protocol. Extensions are
// inert metadata. There are no script, executable or implicit passthrough keys.
type Definition struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Version        string                `json:"version"`
	Family         string                `json:"family"`
	WireVersion    string                `json:"wireVersion"`
	Requires       []string              `json:"requires,omitempty"`
	Capabilities   CapabilitySet         `json:"capabilities"`
	Directions     map[Direction]Mapping `json:"directions"`
	Operations     map[string]Operation  `json:"operations"`
	Expressions    map[string]Expression `json:"expressions,omitempty"`
	Native         NativePolicy          `json:"native"`
	Limits         *Limits               `json:"limits,omitempty"`
	Samples        []Sample              `json:"samples"`
	TaskSamples    []TaskSample          `json:"taskSamples,omitempty"`
	SessionSamples []SessionSample       `json:"sessionSamples,omitempty"`
	Extensions     Object                `json:"extensions,omitempty"`
}

// Mapping implements one direction. Module selects a registered built-in
// adapter; Transform is independently authored and is never mechanically
// inverted to implement another direction.
type Mapping struct {
	Capabilities CapabilitySet `json:"capabilities,omitzero"`
	Module       string        `json:"module,omitempty"`
	After        *Expression   `json:"after,omitempty"`
	Transform    *Expression   `json:"transform,omitempty"`
	Input        *ValueSchema  `json:"input,omitempty"`
	Output       *ValueSchema  `json:"output,omitempty"`
	Rules        []EventRule   `json:"rules,omitempty"`
	UnknownEvent string        `json:"unknownEvent,omitempty"`
}

// EventRule maps a matching wire frame to one event or an ordered event array.
// Cumulative semantics are expressed as item.snapshot, not arbitrary code.
type EventRule struct {
	When Expression `json:"when"`
	Emit Expression `json:"emit"`
}

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
	Kind      string            `json:"kind"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Transport Transport         `json:"transport"`
	Request   Direction         `json:"request,omitempty"`
	Response  Direction         `json:"response,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
	Auth      Credential        `json:"auth"`
	Framing   *Framing          `json:"framing,omitempty"`
	Task      *TaskFlow         `json:"task,omitempty"`
	Session   *SessionConfig    `json:"session,omitempty"`
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
	Done        []string `json:"done,omitempty"`
	EventName   string   `json:"eventName,omitempty"`
	BinaryMedia bool     `json:"binaryMedia,omitempty"`
}

// TaskFlow links separately declared submit/status/result/cancel operations.
// Uncertain submission behavior is owned by the transport, not expressions.
type TaskFlow struct {
	Status            string  `json:"status"`
	Result            string  `json:"result"`
	Cancel            string  `json:"cancel,omitempty"`
	IdempotencyHeader string  `json:"idempotencyHeader,omitempty"`
	Decode            Mapping `json:"decode"`
	Encode            Mapping `json:"encode"`
	Control           Mapping `json:"control"`
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

// Identity returns the wire identity associated with this definition revision.
func (definition Definition) Identity() Identity {
	return Identity{Family: definition.Family, WireVersion: definition.WireVersion, DefinitionID: definition.ID, Revision: definition.Version}
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
	present   map[string]bool
	Op        string                `json:"op"`
	From      string                `json:"from,omitempty"`
	Path      string                `json:"path,omitempty"`
	Value     Value                 `json:"value,omitzero"`
	Fields    map[string]Expression `json:"fields,omitempty"`
	Items     []Expression          `json:"items,omitempty"`
	Source    *Expression           `json:"source,omitempty"`
	Body      *Expression           `json:"body,omitempty"`
	When      *Expression           `json:"when,omitempty"`
	Then      *Expression           `json:"then,omitempty"`
	Otherwise *Expression           `json:"otherwise,omitempty"`
	Cases     map[string]Expression `json:"cases,omitempty"`
	Values    map[string]Value      `json:"values,omitempty"`
	To        JSONType              `json:"to,omitempty"`
	Key       string                `json:"key,omitempty"`
	Policy    string                `json:"policy,omitempty"`
	Ref       string                `json:"ref,omitempty"`
	Required  bool                  `json:"required,omitempty"`
}
