package protocol

import (
	"fmt"
	"slices"
)

const (
	geminiLowReasoningBudget    int64 = 1024
	geminiMediumReasoningBudget int64 = 8192
	geminiHighReasoningBudget   int64 = 24576
	anthropicMinReasoningBudget int64 = 1024
	anthropicDefaultOutputLimit int64 = 8192
)

type reasoningOptions struct {
	SourceCodec string `json:"sourceCodec"`
	TargetCodec string `json:"targetCodec"`
}

func (c *CompiledConversion) reasoningControls(req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var opts reasoningOptions
	if err := rule.Value.Decode(&opts); err != nil {
		return err
	}
	if err := NormalizeReasoningParameters(req, opts.SourceCodec, false); err != nil {
		return err
	}
	cap := ResolveReasoningCapabilities(opts.TargetCodec, route.Model)
	if route.Reasoning != nil {
		cap = *route.Reasoning
	}
	if err := cap.Validate(); err != nil {
		return err
	}
	if err := c.reasoningOutputLimit(req, cap, opts.TargetCodec, route, rule, sink); err != nil {
		return err
	}
	if opts.SourceCodec != opts.TargetCodec {
		for _, key := range []string{"reasoning_effort", "responses_reasoning", "anthropic_thinking", "anthropic_output_config", "gemini_thinking"} {
			if req.Parameters[key].IsNull() {
				delete(req.Parameters, key)
			}
		}
	}
	if req.Reasoning == nil {
		return nil
	}
	r := req.Reasoning
	if opts.SourceCodec != opts.TargetCodec {
		if r.Effort.IsNull() {
			r.Effort = Value{}
		}
		if r.Budget.IsNull() {
			r.Budget = Value{}
		}
		if r.Summary.IsNull() {
			r.Summary = Value{}
		}
	}
	loss := func(at, reason string) error {
		return candidateFailure(c.issue(rule, ConversionRequest, route, at, reason, sink, true))
	}
	if opts.SourceCodec == opts.TargetCodec && cap.Kind == "unknown" && req.Source != AgentIdentity() {
		return nil
	}
	if r.Mode == "disabled" && cap.Kind != "none" && (cap.Kind == "unknown" || !cap.Disable) {
		rule.Action = "reject"
		return candidateFailure(c.issue(rule, ConversionRequest, route, "/reasoning/mode", fmt.Sprintf("model %q cannot disable thinking with the confirmed target capabilities", route.Model), sink, true))
	}
	if cap.Kind == "none" && r.Mode == "disabled" && (!controlPresent(r.Effort) || controlText(r.Effort) == "none") {
		r.Mode, r.Effort, r.EffortScope, r.Budget = "", Value{}, "", Value{}
		if controlText(r.Summary) == "omitted" {
			r.Summary = Value{}
		}
	}
	if (cap.Kind == "unknown" || cap.Kind == "none") && (r.Mode != "" || controlPresent(r.Effort) || controlPresent(r.Budget)) {
		if err := loss("/reasoning", fmt.Sprintf("model %q has %s thinking capability; controls were omitted", route.Model, cap.Kind)); err != nil {
			return err
		}
		r.Mode, r.Effort, r.EffortScope, r.Budget, r.Interleaved = "", Value{}, "", Value{}, false
	}
	if controlPresent(r.Effort) {
		scope := "reasoning"
		if opts.TargetCodec == "anthropic" {
			scope = "generation"
		}
		if scope != r.EffortScope {
			if err := loss("/reasoning/effort", fmt.Sprintf("effort scope changes from %s to %s", r.EffortScope, scope)); err != nil {
				return err
			}
			r.EffortScope = scope
		}
	}
	if r.Interleaved && (opts.TargetCodec != "anthropic" || !cap.Interleaved || !route.InterleavedThinking) {
		if err := loss("/reasoning/interleaved", "target does not support the requested manual interleaved thinking"); err != nil {
			return err
		}
		r.Interleaved = false
	}
	// Missing defaults belong to the selected model. Never fabricate an effort
	// merely because the source omitted its controls.
	if r.Mode == "between_tools" && (opts.TargetCodec != "anthropic" || len(cap.BetweenToolsLevels) == 0) {
		if err := loss("/reasoning/mode", "target cannot limit thinking to between tool calls; using automatic thinking"); err != nil {
			return err
		}
		r.Mode = "auto"
	}
	isManual := opts.TargetCodec == "anthropic" && (cap.Kind == "budget" || (cap.Manual && controlPresent(r.Budget) && controlInt(r.Budget) > 0)) && r.Mode != "disabled" && r.Mode != ""
	if opts.TargetCodec == "gemini" && cap.Kind == "budget" {
		if err := projectGeminiReasoningBudget(r, cap, loss); err != nil {
			return err
		}
	} else if isManual {
		if err := c.manualReasoningBudget(req, cap, route, rule, sink); err != nil {
			return err
		}
	} else if err := projectReasoningMode(r, cap, opts, loss); err != nil {
		return err
	}
	if err := projectReasoningLevel(r, cap, opts, route.Model, loss); err != nil {
		return err
	}
	if err := projectReasoningSummary(r, opts, loss); err != nil {
		return err
	}
	if r.Mode == "" && r.Effort.IsZero() && r.Budget.IsZero() && r.Summary.IsZero() {
		req.Reasoning = nil
		return nil
	}
	if err := ValidateReasoningIntent(r); err != nil {
		return candidateFailure(err)
	}
	if _, err := ReasoningParameters(req, opts.TargetCodec); err != nil {
		return candidateFailure(err)
	}
	return nil
}

func (c *CompiledConversion) manualReasoningBudget(req *Request, cap ReasoningCapabilities, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	r := req.Reasoning
	loss := func(at, reason string) error {
		return candidateFailure(c.issue(rule, ConversionRequest, route, at, reason, sink, true))
	}
	limit := req.Parameters["max_output_tokens"]
	// reasoningOutputLimit has already validated and filled the required limit.
	isExplicit := !req.OutputLimitDefault
	maxOutput := controlInt(limit)
	minimum := max(anthropicMinReasoningBudget, cap.MinBudget)
	if !r.Interleaved && maxOutput <= minimum {
		if isExplicit || cap.MaxOutputTokens <= minimum {
			rule.Action = "reject"
			return candidateFailure(c.issue(rule, ConversionRequest, route, "/parameters/max_output_tokens", fmt.Sprintf("manual thinking cannot fit output limit %d with minimum budget %d; explicit limits are preserved and system defaults require a confirmed expansion bound", maxOutput, minimum), sink, true))
		}
		maxOutput = minimum + 1
	}
	budget := controlInt(r.Budget)
	if !controlPresent(r.Budget) || budget <= 0 {
		percent := int64(80)
		switch controlText(r.Effort) {
		case "minimal":
			percent = 5
		case "low":
			percent = 20
		case "medium":
			percent = 50
		case "xhigh", "max":
			percent = 95
		}
		budget = max(minimum, maxOutput/100*percent+maxOutput%100*percent/100)
		if err := loss("/reasoning/budget", fmt.Sprintf("thinking intent approximated by budget %d (%d%% of output limit, subject to minimum)", budget, percent)); err != nil {
			return err
		}
	}
	if !isExplicit && !r.Interleaved && budget >= maxOutput && cap.MaxOutputTokens > budget {
		maxOutput = budget + 1
	}
	after := max(budget, minimum)
	if cap.MaxBudget > 0 {
		after = min(after, cap.MaxBudget)
	}
	if !r.Interleaved {
		after = min(after, maxOutput-1)
	}
	if after != budget {
		if err := loss("/reasoning/budget", fmt.Sprintf("budget adjusted from %d to %d", budget, after)); err != nil {
			return err
		}
	}
	r.Mode, r.Budget = "enabled", controlValue(after)
	req.Parameters["max_output_tokens"] = controlValue(maxOutput)
	if !isExplicit {
		req.OutputLimitDefault = true
		c.normalized(rule, route, "/parameters/max_output_tokens", fmt.Sprintf("system output default resolved to %d", maxOutput), sink)
	}
	return nil
}

func budgetEffort(budget int64) string {
	if budget <= geminiLowReasoningBudget {
		return "low"
	}
	if budget <= geminiMediumReasoningBudget {
		return "medium"
	}
	return "high"
}

func closestReasoningLevel(requested string, supported []string) string {
	if slices.Contains(supported, requested) {
		return requested
	}
	order := []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	index := slices.Index(order, requested)
	for i := index - 1; i >= 0; i-- {
		if slices.Contains(supported, order[i]) {
			return order[i]
		}
	}
	for _, level := range order {
		if slices.Contains(supported, level) {
			return level
		}
	}
	return ""
}

func (c *CompiledConversion) reasoningOutputLimit(req *Request, cap ReasoningCapabilities, codec string, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	raw := req.Parameters["max_output_tokens"]
	if controlPresent(raw) {
		var n int64
		if raw.Decode(&n) != nil || n < 0 {
			return streamIssue(InvalidInput, "/parameters/max_output_tokens", "output limit must be a nonnegative integer")
		}
	}
	if !controlPresent(raw) && codec != "anthropic" {
		return nil
	}
	n := controlInt(raw)
	if !controlPresent(raw) {
		n = anthropicDefaultOutputLimit
		req.OutputLimitDefault = true
	}
	if cap.MaxOutputTokens > 0 && n > cap.MaxOutputTokens {
		if !req.OutputLimitDefault {
			rule.Action = "reject"
			return candidateFailure(c.issue(rule, ConversionRequest, route, "/parameters/max_output_tokens", fmt.Sprintf("explicit output limit %d exceeds confirmed hard limit %d", n, cap.MaxOutputTokens), sink, true))
		}
		n = cap.MaxOutputTokens
	}
	if !controlPresent(raw) || n != controlInt(raw) {
		c.normalized(rule, route, "/parameters/max_output_tokens", fmt.Sprintf("required system output default resolved to %d", n), sink)
	}
	if req.Parameters == nil {
		req.Parameters = Object{}
	}
	req.Parameters["max_output_tokens"] = controlValue(n)
	return nil
}

func validateReasoningOutputLimit(req *Request, cap ReasoningCapabilities, codec string) error {
	raw := req.Parameters["max_output_tokens"]
	if !controlPresent(raw) {
		if codec == "anthropic" {
			return streamIssue(InvalidInput, "/parameters/max_output_tokens", "target requires an output limit")
		}
		return nil
	}
	var n int64
	if raw.Decode(&n) != nil || n < 0 {
		return streamIssue(InvalidInput, "/parameters/max_output_tokens", "output limit must be a nonnegative integer")
	}
	if cap.MaxOutputTokens > 0 && n > cap.MaxOutputTokens {
		return streamIssue(InvalidInput, "/parameters/max_output_tokens", fmt.Sprintf("output limit %d exceeds confirmed hard limit %d", n, cap.MaxOutputTokens))
	}
	return nil
}

func projectGeminiReasoningBudget(r *ReasoningIntent, cap ReasoningCapabilities, loss func(string, string) error) error {
	if controlPresent(r.Budget) && controlPresent(r.Effort) {
		if err := loss("/reasoning/effort", "target cannot preserve generation effort alongside the thinking budget"); err != nil {
			return err
		}
		r.Effort = Value{}
	}
	if r.Mode == "disabled" {
		r.Budget, r.Effort = controlValue(int64(0)), Value{}
	} else if r.Mode == "auto" {
		if controlPresent(r.Effort) {
			if err := loss("/reasoning/effort", "target cannot preserve generation effort alongside automatic thinking"); err != nil {
				return err
			}
		}
		r.Budget, r.Effort = controlValue(int64(-1)), Value{}
	} else if !controlPresent(r.Budget) && controlPresent(r.Effort) {
		effort := controlText(r.Effort)
		budget := geminiHighReasoningBudget
		if effort == "minimal" || effort == "low" {
			budget = geminiLowReasoningBudget
		}
		if effort == "medium" {
			budget = geminiMediumReasoningBudget
		}
		if err := loss("/reasoning/budget", fmt.Sprintf("effort %s approximated by budget %d", effort, budget)); err != nil {
			return err
		}
		r.Budget, r.Effort = controlValue(budget), Value{}
	}
	if r.Mode == "enabled" && !controlPresent(r.Budget) && !controlPresent(r.Effort) {
		if err := loss("/reasoning/mode", "enabled thinking resolved to automatic target budget"); err != nil {
			return err
		}
		r.Mode, r.Budget = "auto", controlValue(int64(-1))
	}
	if controlPresent(r.Budget) && controlInt(r.Budget) > 0 {
		before := controlInt(r.Budget)
		after := max(before, cap.MinBudget)
		if cap.MaxBudget > 0 {
			after = min(after, cap.MaxBudget)
		}
		if before != after {
			if err := loss("/reasoning/budget", fmt.Sprintf("budget adjusted from %d to %d", before, after)); err != nil {
				return err
			}
			r.Budget = controlValue(after)
		}
	}
	return nil
}

func projectReasoningMode(r *ReasoningIntent, cap ReasoningCapabilities, opts reasoningOptions, loss func(string, string) error) error {
	if controlPresent(r.Budget) && controlInt(r.Budget) > 0 {
		effort := budgetEffort(controlInt(r.Budget))
		if controlPresent(r.Effort) { // A numeric thinking budget takes precedence only where both cannot be expressed.
			if err := loss("/reasoning/effort", "target cannot preserve both effort and numeric budget; budget determines the effective effort"); err != nil {
				return err
			}
		}
		if err := loss("/reasoning/budget", fmt.Sprintf("budget %d approximated by effort %s", controlInt(r.Budget), effort)); err != nil {
			return err
		}
		r.Effort = StringValue(effort)
	}
	r.Budget = Value{}
	if r.Mode == "disabled" {
		if opts.TargetCodec != "anthropic" {
			if controlPresent(r.Effort) && controlText(r.Effort) != "none" {
				if err := loss("/reasoning/effort", "target cannot preserve generation effort with disabled reasoning"); err != nil {
					return err
				}
			}
			r.Effort = StringValue("none")
		}
	} else if r.Mode == "auto" && opts.TargetCodec != "anthropic" {
		if controlPresent(r.Effort) {
			if err := loss("/reasoning/mode", "target cannot separately preserve automatic thinking and generation effort; retaining effort"); err != nil {
				return err
			}
		}
		if !controlPresent(r.Effort) && cap.DefaultMode == "disabled" {
			if err := loss("/reasoning/mode", "target uses its default effort in place of automatic budget selection"); err != nil {
				return err
			}
			r.Effort = StringValue(cap.DefaultEffort)
		}
	} else if opts.TargetCodec == "anthropic" && cap.Kind == "adaptive" && r.Mode == "enabled" {
		r.Mode = "auto"
	}
	return nil
}

func projectReasoningLevel(r *ReasoningIntent, cap ReasoningCapabilities, opts reasoningOptions, model string, loss func(string, string) error) error {
	if controlPresent(r.Effort) && controlText(r.Effort) != "none" {
		before := controlText(r.Effort)
		levels := cap.Levels
		if r.Mode == "disabled" && len(cap.DisabledLevels) > 0 {
			levels = cap.DisabledLevels
		}
		if r.Mode == "between_tools" {
			levels = cap.BetweenToolsLevels
		}
		after := closestReasoningLevel(before, levels)
		if after != before {
			if err := loss("/reasoning/effort", fmt.Sprintf("effort %s adjusted to %q for model %s", before, after, model)); err != nil {
				return err
			}
			if after == "" {
				r.Effort = Value{}
			} else {
				r.Effort = StringValue(after)
			}
		}
	}
	if opts.TargetCodec != "anthropic" && cap.Kind == "level" {
		if r.Mode == "enabled" && !controlPresent(r.Effort) && cap.DefaultMode == "disabled" {
			r.Effort = StringValue(cap.DefaultEffort)
		}
		if controlPresent(r.Effort) {
			r.Mode = "enabled"
			if controlText(r.Effort) == "none" {
				r.Mode = "disabled"
			}
		} else if r.Mode == "auto" {
			if err := loss("/reasoning/mode", "target uses its default effort in place of automatic budget selection"); err != nil {
				return err
			}
			r.Mode = ""
		}
	}
	if !controlPresent(r.Effort) {
		r.EffortScope = ""
	} else {
		r.EffortScope = "reasoning"
		if opts.TargetCodec == "anthropic" {
			r.EffortScope = "generation"
		}
	}
	return nil
}

func projectReasoningSummary(r *ReasoningIntent, opts reasoningOptions, loss func(string, string) error) error {
	if controlPresent(r.Summary) && opts.SourceCodec != opts.TargetCodec {
		before, after := controlText(r.Summary), controlText(r.Summary)
		switch opts.TargetCodec {
		case "openai-chat":
			after = ""
		case "responses":
			if before == "omitted" {
				after = ""
			} else {
				after = "auto"
			}
		case "anthropic":
			if before != "omitted" {
				after = "summarized"
			}
		case "gemini":
			if before != "omitted" {
				after = "auto"
			}
		}
		if before != after && (before != "auto" || after != "summarized") {
			if err := loss("/reasoning/summary", fmt.Sprintf("summary preference %q mapped to %q", before, after)); err != nil {
				return err
			}
		}
		if after == "" {
			r.Summary = Value{}
		} else {
			r.Summary = StringValue(after)
		}
	}
	if opts.TargetCodec == "anthropic" && r.Mode == "" && controlPresent(r.Summary) {
		if err := loss("/reasoning/summary", "target requires a thinking mode for display controls; display preference omitted without enabling thinking"); err != nil {
			return err
		}
		r.Summary = Value{}
	}
	return nil
}
