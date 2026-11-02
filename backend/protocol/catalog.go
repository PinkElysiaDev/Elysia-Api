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
	{"read", []string{"from", "name", "path", "required"}, nil, AnyType, "Read an RFC 6901 pointer from input, item, root, context or a named binding; preserve missing and null."},
	{"let", []string{"name", "source", "body"}, []string{"name", "source", "body"}, AnyType, "Evaluate source once and bind its immutable value by name within body; nested bindings may shadow outer names."},
	{"with_input", []string{"source", "body"}, []string{"source", "body"}, AnyType, "Evaluate body with the selected immutable input without encoding it; root remains the original document, and only original reads retain wire provenance."},
	{"track_tool", []string{"name", "source", "body"}, []string{"name", "source", "body"}, StringType, "Stage bounded JSON/text tool arguments with explicit snapshot, done and emit flags; emit returns every previously withheld byte once."},
	{"track_text", []string{"name", "source", "body", "policy"}, []string{"name", "source", "body", "policy"}, StringType, "Stage a text delta or append-only snapshot for a stable item key and fixed channel, returning only its new suffix."},
	{"track_array", []string{"name", "source", "body", "policy"}, []string{"name", "source", "body", "policy"}, ArrayType, "Track array deltas or snapshots by semantic digest and member count, returning only new members without retaining the forwarded prefix."},
	{"track_position", []string{"name", "source", "body"}, []string{"name", "source", "body"}, IntegerType, "Append a nonnegative length for an item to a fixed output channel; retain coalesced position intervals and return the channel end."},
	{"project_range", []string{"name", "source", "body"}, []string{"name", "source", "body"}, ObjectType, "Project an item's declared start/end range into a tracked output channel; reject ranges crossing other items' output."},
	{"json_digest", []string{"source"}, []string{"source"}, StringType, "Hash a present JSON value by exact semantic content without encoding it. Missing stays missing; null, member types and exact numbers remain distinct."},
	{"buffer_text", []string{"name", "source", "body", "operation"}, []string{"name", "source", "operation"}, StringType, "Retain bounded text by item and fixed channel. Append/snapshot return the suffix; read materializes an immutable whole, length reads byte count, release discards it."},
	{"buffer_array", []string{"name", "source", "body", "operation"}, []string{"name", "source", "operation"}, ArrayType, "Retain a bounded array by item and fixed channel. Append/snapshot return added members; read shares an immutable array, length reads member count, release discards it."},
	{"read_delivered_item", []string{"namespace", "source"}, []string{"namespace", "source"}, ObjectType, "Read entity, payload type and completion for a delivered item in a fixed wire namespace; unsent or rejected state is never readable."},
	{"resume_text", []string{"namespace", "name", "source", "body"}, []string{"namespace", "name", "source", "body"}, AnyType, "Initialize a text tracker from an open delivered entity in a fixed namespace, cloning its digest and byte count; success yields missing."},
	{"resume_tool", []string{"namespace", "name", "source", "body"}, []string{"namespace", "name", "source", "body"}, AnyType, "Initialize a JSON argument tracker from an open delivered entity, owning its prefix once and marking it as sent; explicit placeholders remain replaceable by real input."},
	{"pointer_join", []string{"source", "items"}, []string{"source", "items"}, StringType, "Append decoded string segments to an RFC 6901 pointer, escaping each segment without reading or writing the selected document."},
	{"resolve_item", []string{"source"}, []string{"source"}, ObjectType, "Resolve item/call/index aliases and an existing parent within the current stream transaction."},
	{"index_values", []string{"name"}, []string{"name"}, ArrayType, "Read current values of a declared persistent index; enumerate explicitly, then sort by the authored numeric position when order matters."},
	{"with_index", []string{"name", "keyMode", "policy", "body"}, []string{"name", "keyMode", "policy", "body"}, AnyType, "Create a lexical raw_json index with last, first or reject duplicate policy; its bounded state lasts only through body."},
	{"remember", []string{"name", "source", "body"}, []string{"name", "source", "body"}, AnyType, "Evaluate a raw JSON key from source and a value from body, record the value, and return this evaluated value without materializing containers."},
	{"recall", []string{"name", "source", "policy"}, []string{"name", "source", "policy"}, AnyType, "Read a lexical index by raw JSON key; explicitly omit or reject a missing key."},
	{"forget", []string{"name", "source"}, []string{"name", "source"}, BooleanType, "Remove a raw JSON key from a lexical index, releasing its logical entry and byte budget; return whether it was present."},
	{"index_size", []string{"name"}, []string{"name"}, IntegerType, "Read the number of live entries in a lexical or declared persistent index, including this transaction's writes, without enumerating values."},
	{"index", nil, nil, IntegerType, "Read the zero-based position in the immediate iteration source array, not an original wire location."},
	{"literal", []string{"value"}, []string{"value"}, AnyType, "Return declared JSON without number coercion."},
	{"omit", nil, nil, AnyType, "Explicitly omit an object field or array-map result."},
	{"reject", []string{"path", "source", "reason", "diagnostic"}, []string{"reason", "diagnostic"}, AnyType, "Stop the selected branch with a static classification and reason. Choose one diagnostic location: a literal path or a string pointer read from source."},
	{"object", []string{"fields"}, nil, ObjectType, "Construct an object; omit only missing expression values."},
	{"check_fields", []string{"source", "keys", "prefixes", "policy", "onUnknown"}, []string{"source", "keys", "policy", "onUnknown"}, ObjectType, "Keep an object unchanged after checking its declared member names and prefixes; reject the first unknown member or its first object leaf using a static diagnostic base."},
	{"array", []string{"items"}, nil, ArrayType, "Construct an ordered array."},
	{"length", []string{"source"}, []string{"source"}, IntegerType, "Count array members without encoding them; missing, null and non-array values are errors."},
	{"utf16_length", []string{"source"}, []string{"source"}, IntegerType, "Count UTF-16 code units in a string without constructing a character array."},
	{"map", []string{"source", "body"}, []string{"source", "body"}, ArrayType, "Map each array element in original order."},
	{"walk", []string{"source", "body"}, []string{"source", "body"}, ArrayType, "Map an array with a bounded recursive body; recurse only through explicitly selected child arrays."},
	{"visit", []string{"source", "body"}, []string{"source", "body"}, AnyType, "Apply a bounded recursive body to one selected value at its original location; recur selects child arrays explicitly."},
	{"recur", []string{"source"}, []string{"source"}, ArrayType, "Apply the nearest lexical walk body to a selected array, retaining input and root; requires an enclosing walk body."},
	{"capture_native", []string{"source"}, []string{"source"}, ObjectType, "Capture a read from the original wire with runtime provenance; decode programs only, omitted when native preservation is disabled."},
	{"capture_source", []string{"source"}, []string{"source"}, ObjectType, "Capture the trusted location of an original wire read without retaining its value; decode programs only, independent of native preservation."},
	{"is_carrier", []string{"source"}, []string{"source"}, BooleanType, "Test whether an original wire location was selected as a gateway continuation carrier; source request programs only."},
	{"replay_native", []string{"source"}, []string{"source"}, AnyType, "Replay a Native value against the bound target, scope and native budget; package encode programs only."},
	{"native_matches", []string{"source", "path", "operation", "name"}, []string{"source", "operation"}, BooleanType, "Classify native provenance as an array_item or named array_item_field below a declared parent pointer and a compatible target wire; this does not authorize replay or check resource scope."},
	{"flatmap", []string{"source", "body"}, []string{"source", "body"}, ArrayType, "Map each element to an array and flatten one level."},
	{"filter", []string{"source", "when"}, []string{"source", "when"}, ArrayType, "Select elements with a strictly boolean predicate."},
	{"group_runs", []string{"source", "when"}, []string{"source", "when"}, ArrayType, "Partition an array into consecutive runs with the same boolean predicate result; retain every original member in order without merging separated runs."},
	{"choose", []string{"source", "cases", "otherwise"}, []string{"source", "cases"}, AnyType, "Select a required branch by string discriminator; no implicit fallback."},
	{"if", []string{"when", "then", "otherwise"}, []string{"when", "then"}, AnyType, "Conditionally emit a value; false without otherwise means omission."},
	{"enum", []string{"source", "values", "otherwise"}, []string{"source", "values"}, AnyType, "Map a string through declared literal values; evaluate otherwise only for an unmapped string, or reject when it is absent."},
	{"cast", []string{"source", "to"}, []string{"source", "to"}, AnyType, "Perform a lossless scalar conversion; never truncate numbers."},
	{"merge", []string{"items", "policy"}, []string{"items", "policy"}, ObjectType, "Explicitly merge objects with reject, first or last collision policy."},
	{"merge_deep", []string{"items", "onConflict"}, []string{"items"}, ObjectType, "Recursively union objects; overlapping non-object values always conflict, even when equal. Optional onConflict classifies only this merge's leaf collisions. Arrays are atomic. Missing, null and non-object operands fail."},
	{"check_usage", []string{"source"}, []string{"source"}, AnyType, "Check existing core Usage arithmetic and retain its diagnostics; return the original immutable value without normalizing, encoding or emitting an event."},
	{"check_links", []string{"source", "policy", "required"}, []string{"source", "policy"}, BooleanType, "Check ordered define/resolve/barrier records with key, optional group and diagnostic path. key policy requires explicit keys; unique_group resolves an absent key only through one pending group member. required demands no unresolved definitions at completion."},
	{"merge_usage", []string{"items"}, []string{"items"}, ObjectType, "Merge core Usage snapshots in order, preserving observed counters and recomputing inferred totals; missing or null snapshots are absent, and an entirely absent result is omitted."},
	{"project", []string{"source", "keys", "policy"}, []string{"source", "keys", "policy"}, ObjectType, "Include or exclude declared object keys; retain present null values and never create missing fields."},
	{"keys", []string{"source"}, []string{"source"}, ArrayType, "Return an object's sorted, unique decoded key strings after JSON key normalization, without materializing member values or granting native provenance. Missing, null and non-object sources are errors."},
	{"entries", []string{"source", "policy"}, []string{"source"}, ArrayType, "Read key/value members. Omitted policy sorts and folds decoded duplicate keys; preserve retains original member order and duplicates for explicit validation."},
	{"from_entries", []string{"source", "policy", "onConflict"}, []string{"source", "policy"}, ObjectType, "Construct an object from key/value entries. Explicitly reject duplicate keys, require byte-identical values, or select the last value. No implicit empty value."},
	{"concat", []string{"items"}, []string{"items"}, ArrayType, "Concatenate ordered arrays."},
	{"join", []string{"items", "source"}, nil, StringType, "Concatenate either string expressions in items or a source array of strings, without implicit coercion."},
	{"sort", []string{"source", "key"}, []string{"source", "key"}, ArrayType, "Stable sort by a declared scalar JSON pointer."},
	{"associate", []string{"source", "key"}, []string{"source", "key"}, ObjectType, "Index items by a unique string identity; duplicate identities fail."},
	{"exists", []string{"source"}, []string{"source"}, BooleanType, "Test presence, including explicit null, false and zero."},
	{"present", []string{"source"}, []string{"source"}, BooleanType, "Test presence with a non-null value; explicit null counts as absent."},
	{"type", []string{"source"}, []string{"source"}, StringType, "Read the JSON kind as missing, null, string, array, object, boolean or number without decoding its contents."},
	{"is_integer", []string{"source"}, []string{"source"}, BooleanType, "Test whether a present non-null JSON number decodes as int64, without coercion or truncation."},
	{"integer", []string{"operation", "items"}, []string{"operation", "items"}, AnyType, "Checked int64 add (at least two operands), subtract, multiply, divide, remainder, lt, lte, gt or gte (two operands); evaluate all operands before numeric validation. Division truncates toward zero; zero divisors and overflow fail."},
	{"equal", []string{"items"}, []string{"items"}, BooleanType, "Compare two JSON values semantically with exact numbers."},
	{"equal_text", []string{"items"}, []string{"items"}, BooleanType, "Compare two text operands, each a string or an array of strings, without concatenating them. Evaluate both operands before validating their text; missing, null and non-string members fail."},
	{"all", []string{"items"}, []string{"items"}, BooleanType, "Boolean conjunction, with no truthiness coercion."},
	{"any", []string{"items"}, []string{"items"}, BooleanType, "Boolean disjunction, with no truthiness coercion."},
	{"not", []string{"source"}, []string{"source"}, BooleanType, "Invert a boolean."},
	{"parse_json", []string{"source"}, []string{"source"}, AnyType, "Decode a JSON string; invalid or absent tool arguments fail."},
	{"stringify_json", []string{"source"}, []string{"source"}, StringType, "Encode a present JSON value as a string."},
	{"split_data_uri", []string{"source"}, []string{"source"}, ObjectType, "Split an exact data URI base64 marker into kind, MIME and opaque data without decoding or validating the payload."},
	{"strip_prefix", []string{"source", "value"}, []string{"source", "value"}, StringType, "Remove a required literal string prefix; a mismatching input is an error."},
	{"trim_prefix", []string{"source", "value"}, []string{"source", "value"}, StringType, "Remove a literal string prefix when present; a mismatching input passes through unchanged."},
	{"lower", []string{"source"}, []string{"source"}, StringType, "Convert a string to Unicode lower case without coercing other JSON values."},
	{"has_control", []string{"source"}, []string{"source"}, BooleanType, "Report whether a string contains a Unicode control character (category Cc)."},
	{"quote_string", []string{"source"}, []string{"source"}, StringType, "Quote a string as a Go string literal for exact diagnostic text; do not apply JSON HTML escaping."},
	{"has_prefix", []string{"source", "value"}, []string{"source", "value"}, BooleanType, "Test whether a string begins with a declared literal string; matching is case sensitive."},
	{"contains", []string{"source", "value"}, []string{"source", "value"}, BooleanType, "Test whether a string contains a declared literal string; matching is case sensitive."},
	{"ref", []string{"ref"}, []string{"ref"}, AnyType, "Invoke a named program compiled once for its static scope; recursive references fail, and execution values remain local to each invocation."},
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
	return []Capability{TextCapability, FunctionToolsCapability, FreeTextToolsCapability, ServerToolsCapability, NativeExtensionsCapability, ControlStateCapability, ImagesCapability, AudioCapability, VideoCapability, DocumentsCapability, ReasoningCapability, ReasoningHistoryRetentionCapability, SignaturesCapability, EncryptedReasoningCapability, CacheBreakpointsCapability, CacheKeysCapability, CacheRetentionCapability, CacheResourcesCapability, CacheOptionsCapability, CachePrewarmCapability, SessionsCapability, RealtimeMediaCapability, AsyncJobsCapability, UsageCapability}
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
	return []EventType{SessionStarted, SessionConfigured, ResponseStarted, ItemStarted, ItemDelta, ItemSnapshot, ItemFinished, UsageUpdated, MetadataUpdated, ResponseFinished, OperationFailed, OperationCancelled, MediaReceived, NativeEvent, SessionConfigure, InputAppend, InputCommit, ResponseCreate, ResponseCancel, ToolResultSubmitted, SessionClose}
}

// DiagnosticCatalog lists stable machine-readable compiler/runtime codes.
func DiagnosticCatalog() []IssueCode {
	return []IssueCode{InvalidDefinition, InvalidInput, UnsupportedCapability, UnsupportedNative, ResourceScopeMismatch, InvalidAssociation, InvalidMutation, LimitExceeded, VerificationRequired, VerificationMismatch, IncompleteCoverage, UpstreamContractViolation, ConversionNormalized, ConversionDegraded, ConversionRejected, ContinuationUnavailable, TaskOperationFailed, TaskSubmissionUncertain, ExecutionCancelled, ExecutionTimeout}
}
