package protocol

const responsesMetadataPath = "/metadata"

// ValidateResponsesMetadataFrame checks the auxiliary, response-scoped variant
// of response.metadata. Its object payload stays opaque; headers belong to the
// separate turn-state contract and cannot be mixed into this envelope.
func ValidateResponsesMetadataFrame(frame Value) error {
	fields, err := readControlFields(frame, "")
	if err != nil {
		return err
	}
	if !fields["type"].IsString("response.metadata") {
		return streamIssue(InvalidInput, "/type", "auxiliary metadata requires response.metadata")
	}
	for _, key := range sortedKeys(fields) {
		if key != "type" && key != "metadata" && key != "response_id" && key != "sequence_number" {
			return streamIssue(UnsupportedCapability, "/"+escapePointer(key), "unrecognized response metadata envelope requires an explicit mapping")
		}
	}
	if !fields["metadata"].IsObject() {
		return streamIssue(InvalidInput, "/metadata", "response metadata requires an object")
	}
	var id string
	if fields["response_id"].Decode(&id) != nil || fields["response_id"].IsNull() || id == "" {
		return streamIssue(InvalidInput, "/response_id", "response metadata requires a nonempty response ID")
	}
	var sequence int64
	if fields["sequence_number"].Decode(&sequence) != nil || fields["sequence_number"].IsNull() || sequence < 0 {
		return streamIssue(InvalidInput, "/sequence_number", "response metadata requires a nonnegative integer sequence")
	}
	return nil
}

// Auxiliary metadata has no generated result. Only this validated native shape
// can be omitted or ignored by a generation collector; other native events keep
// their existing capability and collection requirements.
func isResponsesMetadataEvent(event Event) (bool, error) {
	if event.Type != NativeEvent || event.Native == nil || event.Native.Source.Path != responsesMetadataPath {
		return false, nil
	}
	frame := event.Native.Value
	kind, err := readValuePointer(frame, []string{"type"})
	if err != nil || !kind.IsString("response.metadata") {
		return false, nil
	}
	if err := ValidateResponsesMetadataFrame(frame); err != nil {
		return false, err
	}
	source := event.Native.Source
	if source.Protocol.Family == "" || source.Protocol.WireVersion == "" || source.Protocol.DefinitionID == "" || source.Protocol.Revision == "" ||
		source.Direction != DecodeEvent || source.Protocol != event.Source || event.Native.RequestContext != nil ||
		event.ResponsesTurnState != nil || event.Unmapped != nil || !event.SessionID.IsZero() ||
		event.Response != nil || event.Item != nil || event.Request != nil || event.Usage != nil || event.Media != nil ||
		!event.Delta.IsZero() || !event.Error.IsZero() || len(event.Metadata) != 0 ||
		!event.ItemID.IsZero() || !event.ParentID.IsZero() || !event.CallID.IsZero() || event.Index != nil {
		return false, streamIssue(InvalidInput, "/metadata", "auxiliary response metadata must remain isolated from generation data")
	}
	id, _ := readValuePointer(frame, []string{"response_id"})
	if !equalValues(event.ResponseID, id) {
		return false, streamIssue(InvalidAssociation, "/response_id", "auxiliary metadata identity differs from its native frame")
	}
	sequence, _ := readValuePointer(frame, []string{"sequence_number"})
	if !equalValues(event.Sequence, sequence) {
		return false, streamIssue(InvalidAssociation, "/sequence_number", "auxiliary metadata sequence differs from its native frame")
	}
	return true, nil
}

func (c *CompiledConversion) projectResponsesMetadataEvent(event *Event, codec string, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	isMetadata, err := isResponsesMetadataEvent(*event)
	if err != nil || !isMetadata || codec == "responses" {
		return isMetadata, err
	}
	if event.Source != route.Source {
		return true, streamIssue(UnsupportedNative, responsesMetadataPath, "auxiliary metadata source differs from the pinned conversion source")
	}
	if !CheckScope(event.Native.Source.Scope, route.Scope) {
		return true, streamIssue(ResourceScopeMismatch, responsesMetadataPath, "auxiliary metadata scope differs from the conversion target")
	}
	if err := c.issue(rule, ConversionEvent, route, "/metadata", "target cannot express auxiliary response.metadata; omitted metadata does not alter generated content or usage", sink, true); err != nil {
		return true, err
	}
	event.Type, event.Native = MetadataUpdated, nil
	return true, nil
}
