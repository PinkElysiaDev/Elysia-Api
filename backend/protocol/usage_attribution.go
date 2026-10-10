package protocol

import "strconv"

// ParseUsageAttribution recognizes the observed Responses attribution ledger.
// Item IDs and request field names only identify accounting buckets. They are
// never interpreted as history references, tool associations or instructions.
// Unknown nested fields retain the original wire boundary at decoding.
func ParseUsageAttribution(value Value) (bool, error) {
	if value.IsZero() {
		return false, nil
	}
	if value.IsNull() {
		return true, nil
	}
	invalid := func(path, reason string) error { return streamIssue(InvalidInput, path, reason) }
	root, err := value.ReadObject()
	if err != nil {
		return false, invalid("/usage/attribution", "attribution must be an object or null")
	}
	known := true
	var entry func(Value, string, bool) (map[string]int64, error)
	entry = func(value Value, path string, allowContent bool) (map[string]int64, error) {
		fields, err := value.ReadObject()
		if err != nil {
			return nil, invalid(path, "attribution bucket must be an object")
		}
		counts := map[string]int64{}
		for _, key := range sortedKeys(fields) {
			switch key {
			case "input_tokens", "output_tokens", "cached_tokens", "cache_write_tokens":
				var count int64
				if fields[key].IsNull() || fields[key].Decode(&count) != nil || count < 0 {
					return nil, invalid(path+"/"+key, "attribution count must be a nonnegative int64")
				}
				counts[key] = count
			case "content":
				if !allowContent {
					known = false
				}
			default:
				known = false
			}
		}
		if input, ok := counts["input_tokens"]; ok {
			for _, name := range []string{"cached_tokens", "cache_write_tokens"} {
				if counts[name] > input {
					return nil, invalid(path+"/"+name, "attributed cache exceeds bucket input")
				}
				input -= counts[name]
			}
		}
		if content := fields["content"]; allowContent && !content.IsZero() {
			parts, err := content.readArray()
			if err != nil {
				return nil, invalid(path+"/content", "attribution content must be an array")
			}
			remaining := map[string]int64{}
			for k, count := range counts {
				remaining[k] = count
			}
			for i, part := range parts {
				at := path + "/content/" + strconv.Itoa(i)
				child, err := entry(part, at, false)
				if err != nil {
					return nil, err
				}
				for name, count := range child {
					if left, ok := remaining[name]; ok {
						if count > left {
							return nil, invalid(at+"/"+name, "attributed content exceeds bucket subtotal")
						}
						remaining[name] -= count
					}
				}
			}
		}
		return counts, nil
	}
	for _, key := range sortedKeys(root) {
		if key != "items" && key != "request_fields" {
			known = false
			continue
		}
		path := "/usage/attribution/" + key
		buckets, err := root[key].ReadObject()
		if err != nil {
			return false, invalid(path, "attribution group must be an object")
		}
		for _, name := range sortedKeys(buckets) {
			if name == "" {
				return false, invalid(path, "attribution bucket name must not be empty")
			}
			if _, err := entry(buckets[name], path+"/"+escapePointer(name), key == "items"); err != nil {
				return false, err
			}
		}
	}
	return known, nil
}

func emptyUsageAttribution(v Value) bool {
	if v.IsNull() {
		return true
	}
	groups, err := v.ReadObject()
	if err != nil {
		return false
	}
	for _, value := range groups {
		fields, err := value.ReadObject()
		if err != nil || len(fields) > 0 {
			return false
		}
	}
	return true
}
