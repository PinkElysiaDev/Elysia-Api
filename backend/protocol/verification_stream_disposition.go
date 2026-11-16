package protocol

import "context"

// Only committed target transactions can authorize a companion projection.
// The caller supplies a private encoding sink after complete wire validation.
func projectStreamVerification(ctx context.Context, compiled *Compiled, conversion *CompiledConversion, route ConversionContext, issues []ConversionIssue, response *Response) error {
	companions := map[string]string{}
	for _, issue := range issues {
		if conversion == nil || issue.Code != ConversionDegraded || issue.Direction != EncodeEvent || issue.Protocol != compiled.identity || issue.PolicyHash != conversion.Hash {
			continue
		}
		if _, exists := conversion.streamDispositions[issue.RuleID]; !exists {
			continue
		}
		options := conversion.options[issue.RuleID].value.(streamDispositionOptions)
		if options.Disposition.ResponseRule != "" {
			companions[options.Disposition.ResponseRule] = options.ResponseRuleHash
		}
	}
	if len(companions) == 0 {
		return nil
	}
	for _, rule := range conversion.Policy.Rules {
		fingerprint, selected := companions[rule.ID]
		if !selected {
			continue
		}
		actual, err := conversionRuleFingerprint(rule)
		if err != nil {
			return err
		}
		if !rule.Enabled || actual != fingerprint || !rule.Match.matchesRoute(route) {
			return streamIssue(ConversionRejected, "/streamDisposition/responseRule", "stream comparison requires the enabled, pinned companion response rule")
		}
		if _, err := conversion.applyCoreControls(ctx, response, route, rule, nil); err != nil {
			return err
		}
		delete(companions, rule.ID)
	}
	if len(companions) != 0 {
		return streamIssue(ConversionRejected, "/streamDisposition/responseRule", "stream comparison companion response rule is missing")
	}
	_, err := measureSemanticValue(response, semanticLimits(compiled.limits))
	return err
}
