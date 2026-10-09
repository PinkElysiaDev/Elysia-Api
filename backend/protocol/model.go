package protocol

// SemanticSchemaVersion changes only when the public semantic contract changes.
const SemanticSchemaVersion = 1

// Direction identifies independently implemented wire operations.
type Direction string

const (
	DecodeRequest       Direction = "decode_request"
	EncodeRequest       Direction = "encode_request"
	DecodeResponse      Direction = "decode_response"
	EncodeResponse      Direction = "encode_response"
	DecodeEvent         Direction = "decode_event"
	EncodeEvent         Direction = "encode_event"
	DecodeClientEvent   Direction = "decode_client_event"
	EncodeUpstreamEvent Direction = "encode_upstream_event"
)

func isEventDirection(direction Direction) bool {
	return direction == DecodeEvent || direction == EncodeEvent || direction == DecodeClientEvent || direction == EncodeUpstreamEvent
}

func isEventDecoder(direction Direction) bool {
	return direction == DecodeEvent || direction == DecodeClientEvent
}

func eventEncoder(direction Direction) Direction {
	if direction == DecodeClientEvent || direction == EncodeUpstreamEvent {
		return EncodeUpstreamEvent
	}
	return EncodeEvent
}

func eventDecoder(direction Direction) Direction {
	if direction == EncodeUpstreamEvent || direction == DecodeClientEvent {
		return DecodeClientEvent
	}
	return DecodeEvent
}

// Identity separates wire compatibility from a user-selected definition ID.
type Identity struct {
	Family       string `json:"family"`
	WireVersion  string `json:"wireVersion"`
	DefinitionID string `json:"definitionId"`
	Revision     string `json:"revision"`
}

// Scope restricts reuse of provider, account, model and session resources.
// Account holds an opaque account identifier, never an API credential.
type Scope struct {
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account,omitempty"`
	Model    string `json:"model,omitempty"`
	Session  string `json:"session,omitempty"`
}

// Provenance records where a node was parsed; Path is an RFC 6901 pointer.
type Provenance struct {
	Protocol  Identity  `json:"protocol"`
	Direction Direction `json:"direction"`
	Path      string    `json:"path"`
	Scope     Scope     `json:"scope,omitzero"`
}

// Native contains an immutable source snapshot, not a second semantic model.
type Native struct {
	Source Provenance `json:"source"`
	Value  Value      `json:"value"`
}

// NodeKind identifies a semantic item independently of its native type name.
type NodeKind string

const (
	MessageNode    NodeKind = "message"
	TextNode       NodeKind = "text"
	ImageNode      NodeKind = "image"
	AudioNode      NodeKind = "audio"
	VideoNode      NodeKind = "video"
	DocumentNode   NodeKind = "document"
	ReasoningNode  NodeKind = "reasoning"
	RefusalNode    NodeKind = "refusal"
	ToolCallNode   NodeKind = "tool_call"
	ToolResultNode NodeKind = "tool_result"
	OpaqueNode     NodeKind = "opaque"
)

// InputKind distinguishes function JSON from free text without coercion.
type InputKind string

// ReasoningForm identifies a summary instead of visible generated thinking.
type ReasoningForm string

const SummaryReasoning ReasoningForm = "summary"

const (
	JSONInput InputKind = "json"
	TextInput InputKind = "text"
)

// ToolInput carries the actual input value; free text is always a JSON string.
type ToolInput struct {
	Kind  InputKind `json:"kind"`
	Value Value     `json:"value,omitzero"`
}

// Resource identifies a provider-scoped file, cache, session or signature.
type Resource struct {
	Kind  string `json:"kind"`
	ID    Value  `json:"id"`
	Scope Scope  `json:"scope"`
}

// CacheIntent preserves a declared cache policy without enabling caching.
// Kind is breakpoint, key, retention, resource, mode, options.ttl or prewarm;
// Location identifies its scope.
// Breakpoint Value holds the policy object without ttl. TTL is the only owner
// of that field: absent removes it, while an explicit null remains null.
// mode is the provider's explicit/implicit selector and options.ttl is its
// minimum lifetime, kept distinct from retention (a maximum) and from the
// breakpoint TTL because the three are independent wire settings.
type CacheIntent struct {
	Kind     string    `json:"kind"`
	Location string    `json:"location"`
	Value    Value     `json:"value,omitzero" description:"For breakpoints: the cache policy object without ttl, or explicit null. No cache intent means no cache policy is added."`
	TTL      Value     `json:"ttl,omitzero" description:"Breakpoint TTL has one semantic owner here. Missing removes the wire ttl; explicit null remains null. Never also place ttl inside value."`
	Resource *Resource `json:"resource,omitempty"`
}

// Node is one ordered content item. Tool calls and results occupy their original
// positions in Children instead of a separate, independently mutable list.
type Node struct {
	Metadata []ResponseMetadata `json:"metadata,omitempty"`
	Kind     NodeKind           `json:"kind"`
	// ReasoningForm distinguishes provider summaries from visible thinking.
	// Summaries keep their ordered text parts in Children, never in Payload.
	ReasoningForm ReasoningForm `json:"reasoningForm,omitempty"`
	Role          Value         `json:"role,omitzero"`
	ID            Value         `json:"id,omitzero"`
	CallID        Value         `json:"callId,omitzero"`
	Name          Value         `json:"name,omitzero"`
	Status        Value         `json:"status,omitzero"`
	Payload       Value         `json:"payload,omitzero"`
	Input         *ToolInput    `json:"input,omitempty"`
	Children      []Node        `json:"children,omitempty"`
	Cache         []CacheIntent `json:"cache,omitempty"`
	Resources     []Resource    `json:"resources,omitempty"`
	Attributes    Object        `json:"attributes,omitempty"`
	Source        *Provenance   `json:"source,omitempty"`
	Native        *Native       `json:"native,omitempty"`
}

// ToolKind describes the client's execution responsibility, not its wire type.
type ToolKind string

const (
	FunctionTool ToolKind = "function"
	FreeTextTool ToolKind = "free_text"
	ServerTool   ToolKind = "server"
	OpaqueTool   ToolKind = "opaque"
)

// Tool defines an available tool. The gateway does not execute client tools.
type Tool struct {
	Kind        ToolKind      `json:"kind"`
	Name        Value         `json:"name,omitzero"`
	Description Value         `json:"description,omitzero"`
	InputSchema Value         `json:"inputSchema,omitzero"`
	Format      Value         `json:"format,omitzero"`
	Options     Object        `json:"options,omitempty"`
	Cache       []CacheIntent `json:"cache,omitempty"`
	Native      *Native       `json:"native,omitempty"`
}

// Request has one ordered content history. Parameters are named semantic
// fields, not an automatic pass-through container for unknown wire extensions.
type Request struct {
	SchemaVersion int           `json:"schemaVersion"`
	Source        Identity      `json:"source"`
	Model         Value         `json:"model,omitzero"`
	Content       []Node        `json:"content"`
	Tools         []Tool        `json:"tools,omitempty"`
	ToolChoice    Value         `json:"toolChoice,omitzero"`
	Parameters    Object        `json:"parameters,omitempty"`
	Cache         []CacheIntent `json:"cache,omitempty"`
	Resources     []Resource    `json:"resources,omitempty"`
	Native        *Native       `json:"native,omitempty"`
	ClientOutput  *ClientOutput `json:"clientOutput,omitempty"`
}

// ClientOutput describes delivery to the caller, independently of generation.
// RawStreamOptions retains absent/null and extensions for same-wire replay.
type ClientOutput struct {
	CollectUsage        bool                    `json:"collectUsage,omitempty"`
	IncludeUsage        *bool                   `json:"includeUsage,omitempty"`
	RawStreamOptions    Value                   `json:"rawStreamOptions,omitzero"`
	RawResponsesInclude Value                   `json:"rawResponsesInclude,omitzero"`
	ResponsesStorage    *ResponsesStorageIntent `json:"responsesStorage,omitempty"`
}

// ResponsesStorageIntent describes API response retrieval, independently of
// usage logs and authenticated continuation fragments.
type ResponsesStorageIntent struct {
	Raw       Value `json:"raw,omitzero"`
	Requested bool  `json:"requested"`
	Effective bool  `json:"effective"`
}

// CounterOrigin distinguishes observed counts from estimates.
type CounterOrigin string

const (
	ObservedCount CounterOrigin = "observed"
	InferredCount CounterOrigin = "inferred"
	// PlaceholderCount exists only in a client projection, never provider billing.
	PlaceholderCount CounterOrigin = "placeholder"
)

// Counter represents a present count, including zero. A nil *Counter is absent.
type Counter struct {
	Count  int64         `json:"count"`
	Origin CounterOrigin `json:"origin"`
}

// Usage normalizes input to include cached reads and cache creation tokens.
// Details retains declared provider counters without inventing missing totals.
type Usage struct {
	Input         *Counter           `json:"input,omitempty"`
	Output        *Counter           `json:"output,omitempty"`
	Total         *Counter           `json:"total,omitempty"`
	CacheRead     *Counter           `json:"cacheRead,omitempty"`
	CacheCreation *Counter           `json:"cacheCreation,omitempty"`
	Details       map[string]Counter `json:"details,omitempty"`
}

// Response is the ordered result of one generation operation.
type Response struct {
	Metadata      []ResponseMetadata `json:"metadata,omitempty"`
	SchemaVersion int                `json:"schemaVersion"`
	Source        Identity           `json:"source"`
	ID            Value              `json:"id,omitzero"`
	Model         Value              `json:"model,omitzero"`
	Status        Value              `json:"status,omitzero"`
	Content       []Node             `json:"content"`
	Usage         *Usage             `json:"usage,omitempty"`
	Error         Value              `json:"error,omitzero"`
	Attributes    Object             `json:"attributes,omitempty"`
	Native        *Native            `json:"native,omitempty"`
}
