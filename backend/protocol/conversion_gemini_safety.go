package protocol

import (
	"fmt"
	"slices"
	"sort"
)

// ParseGeminiSafetySettings checks wire input and values produced by custom
// semantic mappings. Unknown fields/enums remain available to native Gemini;
// the projection below must explicitly account for them before conversion.
func ParseGeminiSafetySettings(raw Value) ([]Object, error) {
	if raw.IsZero() || raw.IsNull() {
		return nil, nil
	}
	values, err := raw.readArray()
	if err != nil {
		return nil, streamIssue(InvalidInput, "/safetySettings", "safetySettings must be an array or null")
	}
	out := make([]Object, len(values))
	seen := map[string]bool{}
	for i, value := range values {
		at := fmt.Sprintf("/safetySettings/%d", i)
		fields, err := value.ReadObject()
		if err != nil {
			return nil, streamIssue(InvalidInput, at, "safety setting must be an object")
		}
		for _, key := range []string{"category", "threshold"} {
			var text string
			if fields[key].IsZero() || fields[key].IsNull() || fields[key].Decode(&text) != nil || text == "" {
				return nil, streamIssue(InvalidInput, at+"/"+key, "safety setting requires a nonempty string")
			}
			if key == "category" {
				if seen[text] {
					return nil, streamIssue(InvalidInput, at+"/category", "only one safety setting is allowed per category")
				}
				seen[text] = true
			}
		}
		out[i] = fields
	}
	return out, nil
}

func (c *CompiledConversion) geminiSafetySettings(req *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	raw := req.Parameters["gemini_safety_settings"]
	settings, err := ParseGeminiSafetySettings(raw)
	if err != nil {
		return err
	}
	var codec string
	_ = rule.Value.Decode(&codec)
	if raw.IsZero() || codec == "gemini" {
		return nil
	}
	reject := rule
	reject.Action = "reject"
	for i, setting := range settings {
		at := fmt.Sprintf("/safetySettings/%d", i)
		keys := make([]string, 0, len(setting))
		for key := range setting {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key != "category" && key != "threshold" {
				return c.issue(reject, ConversionRequest, route, at+"/"+escapePointer(key), "additional safety setting fields have no equivalent mapping", sink, false)
			}
		}
		var category, threshold string
		_ = setting["category"].Decode(&category)
		_ = setting["threshold"].Decode(&threshold)
		if !slices.Contains([]string{"HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_DANGEROUS_CONTENT", "HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_CIVIC_INTEGRITY"}, category) {
			return c.issue(reject, ConversionRequest, route, at+"/category", "unrecognized Gemini safety category has no equivalent target mapping", sink, false)
		}
		if threshold != "BLOCK_NONE" && threshold != "OFF" {
			return c.issue(reject, ConversionRequest, route, at+"/threshold", "target cannot enforce this Gemini category threshold; blocking constraints cannot be omitted", sink, false)
		}
	}
	if len(settings) > 0 {
		if err := c.issue(rule, ConversionRequest, route, "/safetySettings", "target has no Gemini category filter controls; BLOCK_NONE/OFF cannot disable the target's own safety policy, which remains in force", sink, true); err != nil {
			return err
		}
	} else {
		c.normalized(rule, route, "/safetySettings", "empty Gemini safety settings contain no category overrides", sink)
	}
	delete(req.Parameters, "gemini_safety_settings")
	return nil
}
