package protocol

const imageUsagePrefix = "tools.image_generation."

func validateToolUsageArithmetic(usage *Usage) error {
	check := func(totalName string, parts ...string) error {
		total, hasTotal := usage.Details[imageUsagePrefix+totalName]
		if !hasTotal {
			return nil
		}
		remaining := total.Count
		complete := true
		for _, name := range parts {
			part, exists := usage.Details[imageUsagePrefix+name]
			if !exists {
				complete = false
				continue
			}
			if part.Count < 0 || part.Count > remaining {
				return streamIssue(InvalidInput, "/usage/details/"+imageUsagePrefix+totalName, "hosted image token components exceed subtotal")
			}
			remaining -= part.Count
		}
		if complete && remaining != 0 {
			return streamIssue(InvalidInput, "/usage/details/"+imageUsagePrefix+totalName, "hosted image token components disagree with subtotal")
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
