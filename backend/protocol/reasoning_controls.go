package protocol

import (
	"fmt"
	"maps"
	"slices"
)

const ReasoningControlsFeature = "conversion.reasoning.v1"

// ReasoningIntent keeps independent controls independent: Anthropic effort
// also affects generation when thinking is disabled or a budget is present.
type ReasoningIntent struct {
	Mode        string `json:"mode,omitempty"`
	Effort      Value  `json:"effort,omitzero"`
	EffortScope string `json:"effortScope,omitempty"`
	Budget      Value  `json:"budget,omitzero"`
	Summary     Value  `json:"summary,omitzero"`
	Interleaved bool   `json:"interleaved,omitempty"`
}

func controlPresent(v Value) bool { return !v.IsZero() && !v.IsNull() }
func controlText(v Value) string  { var s string; _ = v.Decode(&s); return s }
func controlInt(v Value) int64    { var n int64; _ = v.Decode(&n); return n }
func controlValue(v any) Value    { result, _ := EncodeValue(v); return result }

func ValidateReasoningIntent(r *ReasoningIntent) error {
	if r == nil {
		return nil
	}
	if !slices.Contains([]string{"", "enabled", "disabled", "auto", "between_tools"}, r.Mode) {
		return streamIssue(InvalidInput, "/reasoning/mode", "unknown thinking mode")
	}
	if controlPresent(r.Effort) {
		var effort string
		if r.Effort.Decode(&effort) != nil || !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effort) {
			return streamIssue(InvalidInput, "/reasoning/effort", "unknown or non-string effort")
		}
		if r.EffortScope != "reasoning" && r.EffortScope != "generation" {
			return streamIssue(InvalidInput, "/reasoning/effortScope", "effort requires its reasoning or generation scope")
		}
	}
	if controlPresent(r.Budget) {
		var budget int64
		if r.Budget.Decode(&budget) != nil || budget < -1 {
			return streamIssue(InvalidInput, "/reasoning/budget", "budget must be an integer >= -1 without overflow")
		}
	}
	if controlPresent(r.Budget) && ((r.Mode == "disabled" && controlInt(r.Budget) != 0) || (r.Mode == "auto" && controlInt(r.Budget) != -1)) {
		return streamIssue(InvalidInput, "/reasoning/budget", "budget conflicts with thinking mode")
	}
	if controlPresent(r.Effort) && r.EffortScope == "reasoning" && r.Mode == "disabled" && controlText(r.Effort) != "none" {
		return streamIssue(InvalidInput, "/reasoning/effort", "reasoning effort conflicts with disabled mode")
	}
	if controlPresent(r.Budget) && r.Mode == "enabled" && controlInt(r.Budget) <= 0 {
		return streamIssue(InvalidInput, "/reasoning/budget", "enabled thinking requires a positive budget")
	}
	if r.Interleaved && (r.Mode != "enabled" || !controlPresent(r.Budget) || controlInt(r.Budget) <= 0) {
		return streamIssue(InvalidInput, "/reasoning/interleaved", "interleaved thinking requires manual thinking with a positive budget")
	}
	if controlPresent(r.Summary) {
		var summary string
		if r.Summary.Decode(&summary) != nil || !slices.Contains([]string{"auto", "concise", "detailed", "summarized", "omitted"}, summary) {
			return streamIssue(InvalidInput, "/reasoning/summary", "unknown or non-string summary preference")
		}
	}
	return nil
}

// NormalizeReasoningParameters is also used at the existing Agent/legacy
// parameter boundary. Unknown object members remain in their original bucket.
func NormalizeReasoningParameters(req *Request, codec string, interleaved bool) error {
	if req.Reasoning != nil {
		for key, names := range map[string][]string{"reasoning_effort": nil, "responses_reasoning": {"effort", "summary"}, "anthropic_thinking": {"type", "budget_tokens", "display"}, "anthropic_output_config": {"effort"}, "gemini_thinking": {"thinkingLevel", "thinkingBudget", "includeThoughts"}} {
			v := req.Parameters[key]
			if !controlPresent(v) {
				continue
			}
			if names == nil {
				return streamIssue(InvalidInput, "/parameters/"+key, "conflicting canonical and legacy reasoning controls")
			}
			obj, err := v.ReadObject()
			if err != nil {
				return err
			}
			for _, name := range names {
				if _, present := obj[name]; present {
					return streamIssue(InvalidInput, "/parameters/"+key+"/"+name, "conflicting canonical and legacy reasoning controls")
				}
			}
		}
		return ValidateReasoningIntent(req.Reasoning)
	}
	r := &ReasoningIntent{}
	params := maps.Clone(req.Parameters)
	take := func(key string, names ...string) (Object, error) {
		v := params[key]
		if !controlPresent(v) {
			return Object{}, nil
		}
		object, err := v.ReadObject()
		if err != nil {
			return nil, streamIssue(InvalidInput, "/parameters/"+key, "thinking controls must be an object or null")
		}
		rest := maps.Clone(object)
		for _, name := range names {
			delete(rest, name)
		}
		if len(rest) == 0 {
			delete(params, key)
		} else {
			params[key] = controlValue(rest)
		}
		return object, nil
	}
	switch codec {
	case "openai-chat":
		r.Effort, r.EffortScope = params["reasoning_effort"], "reasoning"
		delete(params, "reasoning_effort")
	case "responses":
		obj, err := take("responses_reasoning", "effort", "summary")
		if err != nil {
			return err
		}
		r.Effort, r.Summary, r.EffortScope = obj["effort"], obj["summary"], "reasoning"
	case "anthropic":
		obj, err := take("anthropic_thinking", "type", "budget_tokens", "display")
		if err != nil {
			return err
		}
		if len(obj) > 0 {
			if obj["type"].Decode(&r.Mode) != nil || !slices.Contains([]string{"enabled", "disabled", "adaptive", "between_tools"}, r.Mode) {
				return streamIssue(InvalidInput, "/thinking/type", "unknown thinking type")
			}
			r.Budget, r.Summary = obj["budget_tokens"], obj["display"]
			if r.Mode == "adaptive" {
				r.Mode = "auto"
			}
			if r.Mode == "enabled" && (!controlPresent(r.Budget) || r.Budget.Decode(new(int64)) != nil || controlInt(r.Budget) < 1024) {
				return streamIssue(InvalidInput, "/thinking/budget_tokens", "manual thinking requires an integer budget of at least 1024")
			}
			if r.Mode != "enabled" && !r.Budget.IsZero() {
				return streamIssue(InvalidInput, "/thinking/budget_tokens", "this thinking mode does not accept a budget")
			}
			if controlPresent(r.Summary) && !slices.Contains([]string{"summarized", "omitted"}, controlText(r.Summary)) {
				return streamIssue(InvalidInput, "/thinking/display", "display must be summarized or omitted")
			}
		}
		output, err := take("anthropic_output_config", "effort")
		if err != nil {
			return err
		}
		r.Effort, r.EffortScope = output["effort"], "generation"
		r.Interleaved = interleaved && len(req.Tools) > 0 && r.Mode == "enabled"
	case "gemini":
		obj, err := take("gemini_thinking", "thinkingLevel", "thinkingBudget", "includeThoughts")
		if err != nil {
			return err
		}
		r.Effort, r.Budget, r.EffortScope = obj["thinkingLevel"], obj["thinkingBudget"], "reasoning"
		if controlPresent(r.Effort) && controlPresent(r.Budget) {
			return streamIssue(InvalidInput, "/generationConfig/thinkingConfig", "thinkingLevel and thinkingBudget cannot coexist")
		}
		if !obj["includeThoughts"].IsZero() {
			var include bool
			if obj["includeThoughts"].IsNull() || obj["includeThoughts"].Decode(&include) != nil {
				return streamIssue(InvalidInput, "/generationConfig/thinkingConfig/includeThoughts", "includeThoughts must be a boolean")
			}
			if include {
				r.Summary = StringValue("auto")
			} else {
				r.Summary = StringValue("omitted")
			}
		}
	}
	if err := ValidateReasoningIntent(r); err != nil {
		return err
	}
	if codec != "anthropic" {
		if controlPresent(r.Effort) {
			r.Mode = "enabled"
			if controlText(r.Effort) == "none" {
				r.Mode = "disabled"
			}
		}
		if controlPresent(r.Budget) {
			switch controlInt(r.Budget) {
			case -1:
				r.Mode = "auto"
			case 0:
				r.Mode = "disabled"
			default:
				r.Mode = "enabled"
			}
		}
	}
	if r.Mode == "" && r.Effort.IsZero() && r.Budget.IsZero() && r.Summary.IsZero() {
		return nil
	}
	if !controlPresent(r.Effort) {
		r.EffortScope = ""
	}
	if err := validateSourceReasoning(codec, r); err != nil {
		return err
	}
	req.Parameters, req.Reasoning = params, r
	return nil
}

// The ingress model is a group name; only the selected candidate supplies capabilities.
func validateSourceReasoning(codec string, r *ReasoningIntent) error {
	if r == nil {
		return nil
	}
	if codec == "responses" && controlPresent(r.Summary) && !slices.Contains([]string{"auto", "concise", "detailed"}, controlText(r.Summary)) {
		return streamIssue(InvalidInput, "/reasoning/summary", "summary must be auto, concise or detailed")
	}
	if codec == "anthropic" && controlText(r.Effort) == "none" {
		return streamIssue(InvalidInput, "/output_config/effort", "effort cannot disable thinking; use thinking.type")
	}
	if codec == "gemini" && controlPresent(r.Effort) && !slices.Contains([]string{"minimal", "low", "medium", "high"}, controlText(r.Effort)) {
		return streamIssue(InvalidInput, "/generationConfig/thinkingConfig/thinkingLevel", "invalid thinking level")
	}
	if codec == "anthropic" {
		if r.Mode != "enabled" && controlPresent(r.Budget) {
			return streamIssue(InvalidInput, "/thinking/budget_tokens", "this thinking mode does not accept a budget")
		}
		if controlPresent(r.Summary) && (r.Mode == "" || !slices.Contains([]string{"summarized", "omitted"}, controlText(r.Summary))) {
			return streamIssue(InvalidInput, "/thinking/display", "display requires a thinking mode and a supported preference")
		}
	}
	if codec == "anthropic" && r.Mode == "enabled" {
		if !controlPresent(r.Budget) || controlInt(r.Budget) < 1024 {
			return streamIssue(InvalidInput, "/thinking/budget_tokens", "manual thinking requires an integer budget of at least 1024")
		}
	}
	return nil
}

func validateReasoningModel(req *Request, codec string, r *ReasoningIntent, cap ReasoningCapabilities) error {
	if err := validateSourceReasoning(codec, r); err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	if codec == "anthropic" && r.Mode == "enabled" {
		if cap.Kind != "unknown" && !cap.Manual {
			return streamIssue(InvalidInput, "/thinking/type", "model does not support manual thinking")
		}
		interleaved := r.Interleaved && (cap.Kind == "unknown" || cap.Interleaved)

		if controlPresent(req.Parameters["max_output_tokens"]) && controlInt(r.Budget) >= controlInt(req.Parameters["max_output_tokens"]) && !interleaved {
			return streamIssue(InvalidInput, "/thinking/budget_tokens", "manual budget must be below max_tokens without supported interleaved thinking")
		}
	}
	if cap.Kind == "unknown" {
		return nil
	}
	if codec == "gemini" && controlPresent(r.Budget) {
		n := controlInt(r.Budget)
		if cap.Kind != "budget" || (n == 0 && !cap.Disable) || (n > 0 && (n < cap.MinBudget || (cap.MaxBudget > 0 && n > cap.MaxBudget))) {
			return streamIssue(InvalidInput, "/generationConfig/thinkingConfig/thinkingBudget", "budget is invalid for the model")
		}
	}
	if codec == "gemini" && controlPresent(r.Effort) && (cap.Kind != "level" || !slices.Contains(cap.Levels, controlText(r.Effort))) {
		return streamIssue(InvalidInput, "/generationConfig/thinkingConfig/thinkingLevel", "level is invalid for the model")
	}
	if codec == "anthropic" && r.Mode == "auto" && cap.Kind != "adaptive" {
		return streamIssue(InvalidInput, "/thinking/type", "model does not support adaptive thinking")
	}
	if (codec == "anthropic" || codec == "openai-chat" || codec == "responses") && controlPresent(r.Effort) && controlText(r.Effort) != "none" && !slices.Contains(cap.Levels, controlText(r.Effort)) {
		return streamIssue(InvalidInput, "/reasoning/effort", "effort is invalid for the model")
	}
	if r.Mode == "between_tools" && len(cap.BetweenToolsLevels) == 0 {
		return streamIssue(InvalidInput, "/thinking/type", "model does not support between_tools thinking")
	}
	effectiveEffort := controlText(r.Effort)
	if effectiveEffort == "" {
		effectiveEffort = cap.DefaultEffort
	}
	if r.Mode == "disabled" && len(cap.DisabledLevels) > 0 && !slices.Contains(cap.DisabledLevels, effectiveEffort) {
		return streamIssue(InvalidInput, "/reasoning/effort", "model does not allow this effort with disabled thinking")
	}
	if r.Mode == "between_tools" && !slices.Contains(cap.BetweenToolsLevels, effectiveEffort) {
		return streamIssue(InvalidInput, "/reasoning/effort", "model does not allow this effort with between_tools thinking")
	}
	if r.Mode == "disabled" && !cap.Disable {
		return streamIssue(InvalidInput, "/reasoning/mode", "model does not support disabled thinking")
	}
	return nil
}

// ReasoningParameters renders already-resolved intent. It never guesses a
// model or performs a lossy conversion behind the policy engine's back.
func ReasoningParameters(req *Request, codec string) (Object, error) {
	if req.Reasoning == nil {
		return req.Parameters, nil
	}
	r := req.Reasoning
	if err := ValidateReasoningIntent(r); err != nil {
		return nil, err
	}
	out := maps.Clone(req.Parameters)
	if out == nil {
		out = Object{}
	}
	merge := func(key string, fields Object) error {
		base := Object{}
		if controlPresent(out[key]) {
			var err error
			base, err = out[key].ReadObject()
			if err != nil {
				return err
			}
		}
		for k, v := range fields {
			if v.IsZero() {
				continue
			}
			if _, exists := base[k]; exists {
				return streamIssue(InvalidInput, "/parameters/"+key+"/"+k, "conflicting typed and legacy reasoning controls")
			}
			base[k] = v
		}
		if len(base) > 0 {
			out[key] = controlValue(base)
		}
		return nil
	}
	switch codec {
	case "openai-chat":
		if !r.Budget.IsZero() || !r.Summary.IsZero() {
			return nil, streamIssue(UnsupportedCapability, "/reasoning", "Chat cannot encode unresolved budget or summary controls")
		}
		if !r.Effort.IsZero() {
			if !out["reasoning_effort"].IsZero() {
				return nil, streamIssue(InvalidInput, "/reasoning/effort", "conflicting typed and legacy effort")
			}
			out["reasoning_effort"] = r.Effort
		}
	case "responses":
		if !r.Budget.IsZero() {
			return nil, streamIssue(UnsupportedCapability, "/reasoning/budget", "Responses cannot encode a numeric thinking budget")
		}
		if err := merge("responses_reasoning", Object{"effort": r.Effort, "summary": r.Summary}); err != nil {
			return nil, err
		}
	case "anthropic":
		fields := Object{"budget_tokens": r.Budget, "display": r.Summary}
		mode := r.Mode
		if mode == "auto" {
			mode = "adaptive"
		}
		if mode != "" {
			fields["type"] = StringValue(mode)
		}
		if err := merge("anthropic_thinking", fields); err != nil {
			return nil, err
		}
		if err := merge("anthropic_output_config", Object{"effort": r.Effort}); err != nil {
			return nil, err
		}
	case "gemini":
		if controlPresent(r.Effort) && controlPresent(r.Budget) {
			return nil, streamIssue(UnsupportedCapability, "/reasoning", "Gemini cannot encode both level and budget")
		}
		fields := Object{"thinkingLevel": r.Effort, "thinkingBudget": r.Budget}
		if controlPresent(r.Summary) {
			fields["includeThoughts"] = controlValue(controlText(r.Summary) != "omitted")
		}
		if err := merge("gemini_thinking", fields); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown reasoning codec %q", codec)
	}
	return out, nil
}
