package protocol

// ValidateResponsesContext is shared by wire decoding and semantic validation
// after authored mappings. It does not remove or translate context constraints.
func ValidateResponsesContext(truncation, previous Value) error {
	if !truncation.IsZero() && !truncation.IsNull() {
		var mode string
		if truncation.Decode(&mode) != nil || (mode != "auto" && mode != "disabled") {
			return streamIssue(InvalidInput, "/truncation", "truncation must be auto, disabled or null")
		}
	}
	if !previous.IsZero() && !previous.IsNull() {
		var id string
		if previous.Decode(&id) != nil || id == "" {
			return streamIssue(InvalidInput, "/previous_response_id", "previous_response_id must be a nonempty string or null")
		}
	}
	return nil
}

func validateRequestContext(request *Request) error {
	if request == nil {
		return nil // CheckRequest reports the missing semantic document.
	}
	return ValidateResponsesContext(request.Parameters["responses_truncation"], request.Parameters["responses_previous_response_id"])
}

func validateContextValue(phase ConversionPhase, value Value) error {
	if phase != ConversionIngress && phase != ConversionRequest {
		return nil
	}
	fields, err := value.ReadObject()
	if err != nil || fields["parameters"].IsZero() || fields["parameters"].IsNull() {
		return err
	}
	parameters, err := fields["parameters"].ReadObject()
	if err != nil {
		return err
	}
	return ValidateResponsesContext(parameters["responses_truncation"], parameters["responses_previous_response_id"])
}

// Validate before routing so an unsupported native reference is not reported as
// an ambiguous account or a generic resource capability error. This never adds
// sessions capability to a definition or its model binding.
func (compiled *Compiled) checkDecodedResponsesContext(request *Request) error {
	if err := validateRequestContext(request); err != nil {
		return err
	}
	if compiled.Codec(DecodeRequest) != "responses" {
		return nil
	}
	previous := request.Parameters["responses_previous_response_id"]
	for _, resource := range request.Resources {
		if resource.Kind != "session" {
			continue
		}
		if err := ValidateResponsesContext(Value{}, resource.ID); err != nil {
			return err
		}
		previous = resource.ID
	}
	if !previous.IsZero() && !previous.IsNull() && !compiled.Capabilities(DecodeRequest)[SessionsCapability] {
		return streamIssue(UnsupportedCapability, "/previous_response_id", "this Responses ingress has no verified previous-response context capability; provide complete input history and omit the reference, or use a verified stateful route")
	}
	return nil
}

func (c *CompiledConversion) normalized(rule ConversionRule, route ConversionContext, at, reason string, sink *DiagnosticSink) {
	sink.Add(ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved",
		Protocol: route.Target, Direction: EncodeRequest, Stage: "conversion.request", Path: at, Reason: reason,
		RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID]})
}

func (c *CompiledConversion) responsesContext(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	if err := validateRequestContext(request); err != nil {
		return err
	}
	var codec string
	_ = rule.Value.Decode(&codec)
	if codec == "responses" {
		return nil
	}
	reject := rule
	reject.Action = "reject"
	previous := request.Parameters["responses_previous_response_id"]
	for _, resource := range request.Resources {
		if resource.Kind == "session" {
			return c.issue(reject, ConversionRequest, route, "/previous_response_id", "target cannot resolve a previous response; complete history restoration is required before conversion", sink, false)
		}
	}
	if !previous.IsZero() && !previous.IsNull() {
		return c.issue(reject, ConversionRequest, route, "/previous_response_id", "target cannot resolve a previous response; complete history restoration is required before conversion", sink, false)
	}
	if !request.Parameters["responses_truncation"].IsZero() {
		return c.issue(reject, ConversionRequest, route, "/truncation", "target has no verified equivalent context truncation behavior; this constraint cannot be omitted", sink, false)
	}
	if previous.IsNull() {
		delete(request.Parameters, "responses_previous_response_id")
		c.normalized(rule, route, "/previous_response_id", "null previous response reference normalized to no history reference", sink)
	}
	return nil
}
