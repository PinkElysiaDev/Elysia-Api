package protocol

// OperationInfo is the single catalog used by compilation, schema, editor help
// and Agent discovery. Fields lists the only legal keys besides op.
type OperationInfo struct {
	Name        string   `json:"name"`
	Fields      []string `json:"fields"`
	Required    []string `json:"required,omitempty"`
	Result      JSONType `json:"result"`
	Description string   `json:"description"`
}

var expressionOperations = []OperationInfo{
	{"read", []string{"from", "path", "required"}, nil, AnyType, "Read an RFC 6901 pointer from input, item, root or context; preserve missing and null."},
	{"literal", []string{"value"}, []string{"value"}, AnyType, "Return declared JSON without number coercion."},
	{"omit", nil, nil, AnyType, "Explicitly omit an object field or array-map result."},
	{"object", []string{"fields"}, nil, ObjectType, "Construct an object; omit only missing expression values."},
	{"array", []string{"items"}, nil, ArrayType, "Construct an ordered array."},
	{"map", []string{"source", "body"}, []string{"source", "body"}, ArrayType, "Map each array element in original order."},
	{"flatmap", []string{"source", "body"}, []string{"source", "body"}, ArrayType, "Map each element to an array and flatten one level."},
	{"filter", []string{"source", "when"}, []string{"source", "when"}, ArrayType, "Select elements with a strictly boolean predicate."},
	{"choose", []string{"source", "cases", "otherwise"}, []string{"source", "cases"}, AnyType, "Select a required branch by string discriminator; no implicit fallback."},
	{"if", []string{"when", "then", "otherwise"}, []string{"when", "then"}, AnyType, "Conditionally emit a value; false without otherwise means omission."},
	{"enum", []string{"source", "values"}, []string{"source", "values"}, AnyType, "Map an explicit enum; an unmapped input is an error."},
	{"cast", []string{"source", "to"}, []string{"source", "to"}, AnyType, "Perform a lossless scalar conversion; never truncate numbers."},
	{"merge", []string{"items", "policy"}, []string{"items", "policy"}, ObjectType, "Explicitly merge objects with reject, first or last collision policy."},
	{"concat", []string{"items"}, []string{"items"}, ArrayType, "Concatenate ordered arrays."},
	{"join", []string{"items", "source"}, nil, StringType, "Concatenate either string expressions in items or a source array of strings, without implicit coercion."},
	{"sort", []string{"source", "key"}, []string{"source", "key"}, ArrayType, "Stable sort by a declared scalar JSON pointer."},
	{"associate", []string{"source", "key"}, []string{"source", "key"}, ObjectType, "Index items by a unique string identity; duplicate identities fail."},
	{"exists", []string{"source"}, []string{"source"}, BooleanType, "Test presence, including explicit null, false and zero."},
	{"present", []string{"source"}, []string{"source"}, BooleanType, "Test presence with a non-null value; explicit null counts as absent."},
	{"equal", []string{"items"}, []string{"items"}, BooleanType, "Compare two JSON values semantically with exact numbers."},
	{"all", []string{"items"}, []string{"items"}, BooleanType, "Boolean conjunction, with no truthiness coercion."},
	{"any", []string{"items"}, []string{"items"}, BooleanType, "Boolean disjunction, with no truthiness coercion."},
	{"not", []string{"source"}, []string{"source"}, BooleanType, "Invert a boolean."},
	{"parse_json", []string{"source"}, []string{"source"}, AnyType, "Decode a JSON string; invalid or absent tool arguments fail."},
	{"stringify_json", []string{"source"}, []string{"source"}, StringType, "Encode a present JSON value as a string."},
	{"strip_prefix", []string{"source", "value"}, []string{"source", "value"}, StringType, "Remove a required literal string prefix; a mismatching input is an error."},
	{"trim_prefix", []string{"source", "value"}, []string{"source", "value"}, StringType, "Remove a literal string prefix when present; a mismatching input passes through unchanged."},
	{"ref", []string{"ref"}, []string{"ref"}, AnyType, "Expand a named expression at compile time; recursive references fail."},
}

// ExpressionCatalog returns an independent copy of mapping operation metadata.
func ExpressionCatalog() []OperationInfo {
	result := make([]OperationInfo, len(expressionOperations))
	for index, operation := range expressionOperations {
		result[index] = operation
		result[index].Fields = append([]string(nil), operation.Fields...)
		result[index].Required = append([]string(nil), operation.Required...)
	}
	return result
}

// CapabilityCatalog lists every semantic feature recognized by this engine.
func CapabilityCatalog() []Capability {
	return []Capability{TextCapability, FunctionToolsCapability, FreeTextToolsCapability, ServerToolsCapability, NativeExtensionsCapability, ImagesCapability, AudioCapability, VideoCapability, DocumentsCapability, ReasoningCapability, SignaturesCapability, EncryptedReasoningCapability, CacheBreakpointsCapability, CacheKeysCapability, CacheRetentionCapability, CacheResourcesCapability, CacheOptionsCapability, CachePrewarmCapability, SessionsCapability, RealtimeMediaCapability, AsyncJobsCapability, UsageCapability}
}

// DirectionCatalog lists independently implementable adapter operations.
func DirectionCatalog() []Direction {
	return []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse, DecodeEvent, EncodeEvent, DecodeClientEvent, EncodeUpstreamEvent}
}

// TransportCatalog describes installed wire framing vocabulary.
func TransportCatalog() []Transport { return []Transport{HTTPJSON, SSE, NDJSON, WebSocket} }

// OperationKindCatalog describes declarative gateway operations.
func OperationKindCatalog() []string {
	return []string{"generate", "session", "submit", "status", "result", "cancel", "models"}
}

// EventCatalog lists the supported semantic event vocabulary.
func EventCatalog() []EventType {
	return []EventType{SessionStarted, SessionConfigured, ResponseStarted, ItemStarted, ItemDelta, ItemSnapshot, ItemFinished, UsageUpdated, ResponseFinished, OperationFailed, OperationCancelled, MediaReceived, NativeEvent, SessionConfigure, InputAppend, InputCommit, ResponseCreate, ResponseCancel, ToolResultSubmitted, SessionClose}
}

// DiagnosticCatalog lists stable machine-readable compiler/runtime codes.
func DiagnosticCatalog() []IssueCode {
	return []IssueCode{InvalidDefinition, InvalidInput, UnsupportedCapability, UnsupportedNative, ResourceScopeMismatch, InvalidAssociation, InvalidMutation, LimitExceeded, VerificationRequired, VerificationMismatch, IncompleteCoverage, UpstreamContractViolation, ConversionDegraded, ConversionRejected, ContinuationUnavailable, TaskOperationFailed, TaskSubmissionUncertain}
}
