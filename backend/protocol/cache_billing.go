package protocol

// ParseCacheBilling recognizes this channel's disclosed cache allocation only.
// The observed and billed counts are independent evidence, not replacements
// for the provider's ordinary cached_tokens or for local pricing decisions.
func ParseCacheBilling(value Value) (bool, error) {
	if value.IsZero() {
		return false, nil
	}
	if value.IsNull() {
		return true, nil
	}
	const base = "/usage/linapi_cache_billing"
	fields, err := value.ReadObject()
	if err != nil {
		return false, streamIssue(InvalidInput, base, "cache billing disclosure must be an object or null")
	}
	if len(fields) == 0 {
		return true, nil
	}
	known := true
	for _, key := range sortedKeys(fields) {
		switch key {
		case "billed_cached_tokens", "observed_cached_tokens", "policy_revision":
			var n int64
			if fields[key].IsNull() || fields[key].Decode(&n) != nil || n < 0 {
				return false, streamIssue(InvalidInput, base+"/"+key, "billing count/revision must be a nonnegative int64")
			}
		case "rule_id", "basis":
			var s string
			if fields[key].IsNull() || fields[key].Decode(&s) != nil || s == "" {
				return false, streamIssue(InvalidInput, base+"/"+key, "billing rule and basis must be nonempty strings")
			}
		default:
			known = false
		}
	}
	for _, key := range []string{"basis", "billed_cached_tokens", "observed_cached_tokens", "policy_revision", "rule_id"} {
		if fields[key].IsZero() {
			known = false
		}
	}
	if fields["basis"] != StringValue("disclosed_billing_allocation") {
		known = false
	}
	return known, nil
}
