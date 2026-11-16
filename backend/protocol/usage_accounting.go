package protocol

import "strconv"

const (
	usageAttributionPath = "/usage/attribution"
	cacheBillingPath     = "/usage/cacheBilling"
	cacheAllocationBasis = "cache-allocation"
)

// ParseUsageAttribution validates canonical accounting buckets. Bucket names
// identify accounting entries, never history references or tool associations.
// It returns false for an absent ledger or an unrecognized semantic field.
func ParseUsageAttribution(value Value) (bool, error) {
	if value.IsZero() || value.IsNull() {
		return value.IsNull(), nil
	}
	groups, err := value.ReadObject()
	if err != nil {
		return false, streamIssue(InvalidInput, usageAttributionPath, "attribution must be an object or null")
	}
	isKnown := true
	for _, group := range sortedKeys(groups) {
		if group != "items" && group != "requestFields" {
			isKnown = false
			continue
		}
		path := usageAttributionPath + "/" + group
		buckets, err := groups[group].ReadObject()
		if err != nil {
			return false, streamIssue(InvalidInput, path, "attribution group must be an object")
		}
		for _, name := range sortedKeys(buckets) {
			if name == "" {
				return false, streamIssue(InvalidInput, path, "attribution bucket name must not be empty")
			}
			_, isBucketKnown, err := readAttributionBucket(buckets[name], path+"/"+escapePointer(name), group == "items")
			if err != nil {
				return false, err
			}
			isKnown = isKnown && isBucketKnown
		}
	}
	return isKnown, nil
}

func readAttributionBucket(value Value, path string, canHaveContent bool) (map[string]int64, bool, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return nil, false, streamIssue(InvalidInput, path, "attribution bucket must be an object")
	}
	counts := map[string]int64{}
	isKnown := true
	for _, name := range sortedKeys(fields) {
		switch name {
		case "input", "output", "cacheRead", "cacheCreation":
			count, err := readAccountingInteger(fields[name], path+"/"+name)
			if err != nil {
				return nil, false, err
			}
			counts[name] = count
		case "content":
			isKnown = isKnown && canHaveContent
		default:
			isKnown = false
		}
	}
	if input, hasInput := counts["input"]; hasInput {
		for _, name := range []string{"cacheRead", "cacheCreation"} {
			if counts[name] > input {
				return nil, false, streamIssue(InvalidInput, path+"/"+name, "attributed cache exceeds bucket input")
			}
			input -= counts[name]
		}
	}
	if content := fields["content"]; canHaveContent && !content.IsZero() {
		isContentKnown, err := checkAttributionContent(content, counts, path+"/content")
		if err != nil {
			return nil, false, err
		}
		isKnown = isKnown && isContentKnown
	}
	return counts, isKnown, nil
}

func checkAttributionContent(value Value, totals map[string]int64, path string) (bool, error) {
	if !value.IsArray() {
		return false, streamIssue(InvalidInput, path, "attribution content must be an array")
	}
	parts, _ := value.readArray()
	remaining := make(map[string]int64, len(totals))
	for name, count := range totals {
		remaining[name] = count
	}
	isKnown := true
	for index, part := range parts {
		partPath := path + "/" + strconv.Itoa(index)
		counts, isPartKnown, err := readAttributionBucket(part, partPath, false)
		if err != nil {
			return false, err
		}
		isKnown = isKnown && isPartKnown
		for _, name := range sortedKeys(counts) {
			count := counts[name]
			if subtotal, hasSubtotal := remaining[name]; hasSubtotal {
				if count > subtotal {
					return false, streamIssue(InvalidInput, partPath+"/"+name, "attributed content exceeds bucket subtotal")
				}
				remaining[name] -= count
			}
		}
	}
	return isKnown, nil
}

// ParseCacheBilling validates a canonical cache allocation disclosure. Billed
// and observed counts are independent evidence; neither replaces model usage.
// It returns false for an absent ledger or an unrecognized allocation contract.
func ParseCacheBilling(value Value) (bool, error) {
	if value.IsZero() || value.IsNull() {
		return value.IsNull(), nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return false, streamIssue(InvalidInput, cacheBillingPath, "cache billing disclosure must be an object or null")
	}
	if len(fields) == 0 {
		return true, nil
	}
	isKnown := true
	for _, name := range sortedKeys(fields) {
		path := cacheBillingPath + "/" + name
		switch name {
		case "billed", "observed", "policyRevision":
			if _, err := readAccountingInteger(fields[name], path); err != nil {
				return false, err
			}
		case "ruleId", "basis":
			var text string
			if fields[name].Decode(&text) != nil || text == "" {
				return false, streamIssue(InvalidInput, path, "billing rule and basis must be nonempty strings")
			}
		default:
			isKnown = false
		}
	}
	for _, name := range []string{"basis", "billed", "observed", "policyRevision", "ruleId"} {
		isKnown = isKnown && !fields[name].IsZero()
	}
	return isKnown && fields["basis"].IsString(cacheAllocationBasis), nil
}

func readAccountingInteger(value Value, path string) (int64, error) {
	var count int64
	if value.IsNull() || value.Decode(&count) != nil || count < 0 {
		return 0, streamIssue(InvalidInput, path, "accounting value must be a nonnegative int64")
	}
	return count, nil
}
