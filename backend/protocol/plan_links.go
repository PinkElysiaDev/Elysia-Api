package protocol

import "fmt"

type linkRecord struct {
	kind, key, group, path string
}

type linkDefinition struct {
	group, path string
	isConsumed  bool
}

// checkLinks consumes only the declared identity records. The caller selects
// real wire locations; argument bodies, media and native snapshots stay outside
// the association table, which lives only for this synchronous check.
func (plan *compiledOutputPlan) checkLinks(state evaluation) (plannedOutput, error) {
	input, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	records, err := plannedArrayItems(input.output)
	if err != nil {
		return plannedOutput{}, err
	}
	byKey := make(map[string]*linkDefinition)
	byGroup := make(map[string]map[*linkDefinition]struct{})
	var definitions []*linkDefinition
	retainedBytes, pendingCount := 0, 0
	for _, value := range records {
		if err := state.advance(); err != nil {
			return plannedOutput{}, err
		}
		record, err := readLinkRecord(value, plan.expression.policy)
		if err != nil {
			return plannedOutput{}, err
		}
		if record.kind == "barrier" {
			if pendingCount != 0 {
				return plannedOutput{}, linkFailure(record.path, "pending links must resolve before this boundary", nil)
			}
			continue
		}
		if record.kind == "define" {
			if record.key != "" && byKey[record.key] != nil {
				return plannedOutput{}, linkFailure(record.path, "link identity is already defined", nil)
			}
			limits := state.budget.limits
			if len(definitions) >= limits.StateItems {
				return plannedOutput{}, resourceFailure("links", "items", limits.StateItems, len(definitions)+1)
			}
			retainedBytes += len(record.key) + len(record.group) + len(record.path)
			if retainedBytes > limits.BufferBytes {
				return plannedOutput{}, resourceFailure("links", "bytes", limits.BufferBytes, retainedBytes)
			}
			definition := &linkDefinition{group: record.group, path: record.path}
			definitions = append(definitions, definition)
			pendingCount++
			if record.key != "" {
				byKey[record.key] = definition
			}
			if record.group != "" {
				if byGroup[record.group] == nil {
					byGroup[record.group] = make(map[*linkDefinition]struct{})
				}
				byGroup[record.group][definition] = struct{}{}
			}
			continue
		}
		definition := byKey[record.key]
		if record.key == "" {
			pending := byGroup[record.group]
			if len(pending) != 1 {
				return plannedOutput{}, linkFailure(record.path, "link group must identify exactly one pending definition", nil)
			}
			for entry := range pending {
				definition = entry
			}
		}
		if definition == nil {
			return plannedOutput{}, linkFailure(record.path, "link has no preceding definition", nil)
		}
		if definition.isConsumed {
			return plannedOutput{}, linkFailure(record.path, "link definition is already consumed", nil)
		}
		definition.isConsumed = true
		pendingCount--
		if definition.group != "" {
			delete(byGroup[definition.group], definition)
		}
	}
	if plan.expression.isRequired && pendingCount != 0 {
		for _, definition := range definitions {
			if err := state.advance(); err != nil {
				return plannedOutput{}, err
			}
			if !definition.isConsumed {
				return plannedOutput{}, linkFailure(definition.path, "link definition has no result", nil)
			}
		}
	}
	return plannedLeaf(coreBoolean(true)), nil
}

func readLinkRecord(value JSONOutput, policy string) (linkRecord, error) {
	fields, err := plannedObjectFields(value)
	if err != nil {
		return linkRecord{}, linkFailure("", "link record requires an object", err)
	}
	var kind, key, group, path JSONOutput
	for _, field := range fields {
		switch field.key {
		case "kind":
			kind = field.value
		case "key":
			key = field.value
		case "group":
			group = field.value
		case "path":
			path = field.value
		}
	}
	record := linkRecord{}
	record.path, err = readPlannedString(path)
	if err != nil {
		return record, linkFailure("", "link record requires its wire pointer", err)
	}
	if _, err := parsePointer(record.path); err != nil {
		return record, linkFailure("", "link record requires a valid wire pointer", err)
	}
	record.kind, err = readPlannedString(kind)
	if err != nil || record.kind != "define" && record.kind != "resolve" && record.kind != "barrier" {
		return record, linkFailure(record.path, "link action must be define, resolve or barrier", err)
	}
	if record.kind == "barrier" {
		return record, nil
	}
	for _, field := range []struct {
		name   string
		value  JSONOutput
		target *string
	}{{"key", key, &record.key}, {"group", group, &record.group}} {
		if field.value.IsZero() {
			continue
		}
		*field.target, err = readPlannedString(field.value)
		if err != nil || *field.target == "" {
			return record, linkFailure(record.path, fmt.Sprintf("link %s must be a nonempty string", field.name), err)
		}
	}
	if record.key == "" && (policy == "key" || record.group == "") {
		return record, linkFailure(record.path, "link requires a key or an explicitly enabled group", nil)
	}
	return record, nil
}

func linkFailure(path, reason string, cause error) error {
	return &declaredCheckError{err: &ConversionError{cause: cause, Issues: []ConversionIssue{{
		Code: InvalidAssociation, Severity: SeverityError, Stage: "wire", Path: path, Reason: reason,
	}}}}
}
