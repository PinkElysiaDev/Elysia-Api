package protocol

import "strings"

// ResponsesToolUsageFields declares the channel's separate hosted-tool ledger.
// These counters never contribute to the model's input, output or total tokens.
func ResponsesToolUsageFields() map[string]string {
	return map[string]string{
		"web_search.num_requests":                      "tools.web_search_calls",
		"image_gen.input_tokens":                       "tools.image_generation.input_tokens",
		"image_gen.output_tokens":                      "tools.image_generation.output_tokens",
		"image_gen.total_tokens":                       "tools.image_generation.total_tokens",
		"image_gen.input_tokens_details.image_tokens":  "tools.image_generation.input.image_tokens",
		"image_gen.input_tokens_details.text_tokens":   "tools.image_generation.input.text_tokens",
		"image_gen.output_tokens_details.image_tokens": "tools.image_generation.output.image_tokens",
		"image_gen.output_tokens_details.text_tokens":  "tools.image_generation.output.text_tokens",
	}
}

func IsResponsesToolUsageDetail(name string) bool {
	for _, semantic := range ResponsesToolUsageFields() {
		if semantic == name {
			return true
		}
	}
	return false
}

// Empty containers have no observed counts. Unknown keys and null counters
// cannot be reclassified as harmless empty metadata.
func EmptyResponsesToolUsage(v Value) bool {
	if v.IsNull() {
		return true
	}
	var visit func(Value, string) bool
	visit = func(v Value, prefix string) bool {
		fields, err := v.ReadObject()
		if err != nil {
			return false
		}
		for key, value := range fields {
			path := prefix + key
			knownContainer := false
			for leaf := range ResponsesToolUsageFields() {
				if strings.HasPrefix(leaf, path+".") {
					knownContainer = true
					break
				}
			}
			if !knownContainer || !visit(value, path+".") {
				return false
			}
		}
		return true
	}
	return visit(v, "")
}

func validateResponsesToolUsageArithmetic(usage *Usage) error {
	if usage == nil {
		return nil
	}
	prefix := "tools.image_generation."
	check := func(totalName string, parts ...string) error {
		total, hasTotal := usage.Details[prefix+totalName]
		if !hasTotal {
			return nil
		}
		remaining := total.Count
		complete := true
		for _, name := range parts {
			part, exists := usage.Details[prefix+name]
			if !exists {
				complete = false
				continue
			}
			if part.Count < 0 || part.Count > remaining {
				return streamIssue(InvalidInput, "/usage/details/"+prefix+totalName, "hosted image token components exceed subtotal")
			}
			remaining -= part.Count
		}
		if complete && remaining != 0 {
			return streamIssue(InvalidInput, "/usage/details/"+prefix+totalName, "hosted image token components disagree with subtotal")
		}
		return nil
	}
	if err := check("input_tokens", "input.image_tokens", "input.text_tokens"); err != nil {
		return err
	}
	if err := check("output_tokens", "output.image_tokens", "output.text_tokens"); err != nil {
		return err
	}
	return check("total_tokens", "input_tokens", "output_tokens")
}
