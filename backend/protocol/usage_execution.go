package protocol

import "strconv"

const (
	executionSpeedPath = "/usage/executionSpeed"
	executionUsagePath = "/usage/executions"
)

func validateExecutionUsage(usage *Usage) error {
	if err := validateExecutionSpeed(usage.ExecutionSpeed); err != nil {
		return err
	}
	if usage.Executions.IsZero() || usage.Executions.IsNull() {
		return nil
	}
	if !usage.Executions.IsArray() {
		return streamIssue(InvalidInput, executionUsagePath, "execution usage must be an ordered snapshot array or null")
	}
	entries, _ := usage.Executions.readArray()
	remaining := map[string]int64{}
	for name, counter := range map[string]*Counter{"input": usage.Input, "output": usage.Output, "cacheRead": usage.CacheRead, "cacheCreation": usage.CacheCreation} {
		if counter != nil {
			remaining[name] = counter.Count
		}
	}
	for index, entry := range entries {
		path := executionUsagePath + "/" + strconv.Itoa(index)
		fields, counts, err := readExecutionUsage(entry, path)
		if err != nil {
			return err
		}
		if !fields["topLevelRelation"].IsString("included") {
			continue
		}
		for _, name := range sortedKeys(counts) {
			count := counts[name]
			if total, hasTotal := remaining[name]; hasTotal {
				if count > total {
					return streamIssue(InvalidInput, path+"/"+name, "included executions exceed the corresponding parent count")
				}
				remaining[name] -= count
			}
		}
	}
	return nil
}

func validateExecutionSpeed(value Value) error {
	if value.IsZero() || value.IsNull() || value.IsString("standard") || value.IsString("fast") {
		return nil
	}
	return streamIssue(InvalidInput, executionSpeedPath, "execution speed must be standard, fast or null")
}

func readExecutionUsage(value Value, path string) (Object, map[string]int64, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return nil, nil, streamIssue(InvalidInput, path, "execution usage entry must be an object")
	}
	for _, name := range sortedKeys(fields) {
		switch name {
		case "purpose", "model", "topLevelRelation", "input", "output", "cacheRead", "cacheCreation", "cacheCreationByRetention":
		default:
			return nil, nil, streamIssue(InvalidInput, path+"/"+escapePointer(name), "unknown execution usage field")
		}
	}
	purpose := fields["purpose"]
	if !purpose.IsString("sampling") && !purpose.IsString("context-compaction") && !purpose.IsString("advisor") && !purpose.IsString("fallback-result") {
		return nil, nil, streamIssue(InvalidInput, path+"/purpose", "unknown execution purpose")
	}
	relation := fields["topLevelRelation"]
	if !relation.IsString("included") && !relation.IsString("excluded") && !relation.IsString("unknown") {
		return nil, nil, streamIssue(InvalidInput, path+"/topLevelRelation", "execution relationship must be included, excluded or unknown")
	}
	model := fields["model"]
	if !model.IsZero() && !model.IsNull() && (!model.IsStringValue() || model.IsString("")) {
		return nil, nil, streamIssue(InvalidInput, path+"/model", "execution model must be a reported nonempty string or null")
	}
	counts := make(map[string]int64, 4)
	for _, name := range []string{"input", "output", "cacheRead", "cacheCreation"} {
		count, err := readExecutionCounter(fields[name], path+"/"+name)
		if err != nil {
			return nil, nil, err
		}
		counts[name] = count
	}
	remainingInput := counts["input"]
	for _, name := range []string{"cacheRead", "cacheCreation"} {
		if counts[name] > remainingInput {
			return nil, nil, streamIssue(InvalidInput, path+"/"+name, "execution cache counts exceed inclusive input")
		}
		remainingInput -= counts[name]
	}
	if err := validateExecutionRetention(fields["cacheCreationByRetention"], counts["cacheCreation"], path+"/cacheCreationByRetention"); err != nil {
		return nil, nil, err
	}
	return fields, counts, nil
}

func readExecutionCounter(value Value, path string) (int64, error) {
	fields, err := value.ReadObject()
	if err != nil || len(fields) != 2 || !fields["origin"].IsString(string(ObservedCount)) {
		return 0, streamIssue(InvalidInput, path, "execution counter requires only count and observed origin")
	}
	return readAccountingInteger(fields["count"], path+"/count")
}

func validateExecutionRetention(value Value, total int64, path string) error {
	if value.IsZero() || value.IsNull() {
		return nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return streamIssue(InvalidInput, path, "execution cache retention must be an object or null")
	}
	remaining := total
	for _, name := range sortedKeys(fields) {
		if name != "5m" && name != "1h" {
			return streamIssue(InvalidInput, path+"/"+escapePointer(name), "unknown execution cache retention")
		}
		count, err := readExecutionCounter(fields[name], path+"/"+name)
		if err != nil {
			return err
		}
		if count > remaining {
			return streamIssue(InvalidInput, path+"/"+name, "execution cache retention exceeds cache creation")
		}
		remaining -= count
	}
	if len(fields) == 2 && remaining != 0 {
		return streamIssue(InvalidInput, path, "complete retention breakdown must equal cache creation")
	}
	return nil
}
