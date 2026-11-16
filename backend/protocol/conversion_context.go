package protocol

func validateRequestContext(request *Request) error {
	if request == nil {
		return nil // CheckRequest reports the missing semantic document.
	}
	if requestContextUnchanged(request) {
		return nil
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
	return validateOutputConstraint(request.Parameters["outputConstraint"])
}

// Validate before routing so an unsupported native reference is not reported as
// an ambiguous account or a generic resource capability error. This never adds
// sessions capability to a definition or its model binding.

func (c *CompiledConversion) normalized(rule ConversionRule, route ConversionContext, at, reason string, sink *DiagnosticSink) {
	direction := map[ConversionPhase]Direction{ConversionIngress: DecodeRequest, ConversionRequest: EncodeRequest, ConversionResponse: EncodeResponse, ConversionEvent: EncodeEvent}[rule.Phase]
	sink.Add(ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved",
		Protocol: route.Target, Direction: direction, Stage: "conversion." + string(rule.Phase), Path: at, Reason: reason,
		RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID]})
}
