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
	if err := validateRequestHints(request); err != nil {
		return err
	}
	if err := validateToolResultErrors(request.Content, "/content"); err != nil {
		return err
	}
	if request.Reasoning != nil && request.Reasoning.Interleaved && len(request.Tools) == 0 {
		return streamIssue(InvalidInput, "/reasoning/interleaved", "interleaved thinking requires tools")
	}
	if err := ValidateReasoningIntent(request.Reasoning); err != nil {
		return err
	}
	if _, err := ParseGeminiSafetySettings(request.Parameters["gemini_safety_settings"]); err != nil {
		return err
	}
	return ValidateResponsesContext(request.Parameters["responses_truncation"], request.Parameters["responses_previous_response_id"])
}

func validateContextValue(phase ConversionPhase, value Value) error {
	if phase != ConversionIngress && phase != ConversionRequest {
		return nil
	}
	var req Request
	if err := value.Decode(&req); err != nil {
		return err
	}
	return validateRequestContext(&req)
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
	direction := map[ConversionPhase]Direction{ConversionIngress: DecodeRequest, ConversionRequest: EncodeRequest, ConversionResponse: EncodeResponse, ConversionEvent: EncodeEvent}[rule.Phase]
	sink.Add(ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved",
		Protocol: route.Target, Direction: direction, Stage: "conversion." + string(rule.Phase), Path: at, Reason: reason,
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
			return candidateFailure(c.issue(reject, ConversionRequest, route, "/previous_response_id", "target cannot resolve a previous response; complete history restoration is required before conversion", sink, false))
		}
	}
	if !previous.IsZero() && !previous.IsNull() {
		return candidateFailure(c.issue(reject, ConversionRequest, route, "/previous_response_id", "target cannot resolve a previous response; complete history restoration is required before conversion", sink, false))
	}
	if !request.Parameters["responses_truncation"].IsZero() {
		return candidateFailure(c.issue(reject, ConversionRequest, route, "/truncation", "target has no verified equivalent context truncation behavior; this constraint cannot be omitted", sink, false))
	}
	if previous.IsNull() {
		delete(request.Parameters, "responses_previous_response_id")
		c.normalized(rule, route, "/previous_response_id", "null previous response reference normalized to no history reference", sink)
	}
	return nil
}
