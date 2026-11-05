package protocol

import (
	"fmt"
	"slices"
	"strings"
)

// ObjectFieldDiagnostic classifies an undeclared object member. PathPrefix is
// authored configuration; the remaining pointer comes only from actual keys.
// It shares the static classification and pointer checks used by object merges.
type ObjectFieldDiagnostic MergeConflictDiagnostic

type compiledObjectFieldCheck struct {
	keys       map[string]bool
	prefixes   []string
	isLeaf     bool
	diagnostic *MergeConflictDiagnostic
}

func compileObjectFieldCheck(expression Expression, path string) (*compiledObjectFieldCheck, error) {
	if expression.Policy != "member" && expression.Policy != "leaf" {
		return nil, fmt.Errorf("%s/policy: expected member or leaf", path)
	}
	if expression.OnUnknown == nil {
		return nil, fmt.Errorf("%s/onUnknown: field rejection diagnostic is required", path)
	}
	classification := MergeConflictDiagnostic(*expression.OnUnknown)
	diagnostic, err := compileMergeConflictDiagnostic(&classification, path+"/onUnknown")
	if err != nil {
		return nil, err
	}
	check := &compiledObjectFieldCheck{keys: make(map[string]bool, len(expression.Keys)), prefixes: slices.Clone(expression.Prefixes), isLeaf: expression.Policy == "leaf", diagnostic: diagnostic}
	for _, key := range expression.Keys {
		if check.keys[key] {
			return nil, fmt.Errorf("%s/keys: duplicate member name", path)
		}
		check.keys[key] = true
	}
	for index, prefix := range check.prefixes {
		if prefix == "" || slices.Contains(check.prefixes[:index], prefix) {
			return nil, fmt.Errorf("%s/prefixes: prefixes must be nonempty and unique", path)
		}
	}
	return check, nil
}

func (check *compiledObjectFieldCheck) allows(key string) bool {
	if check.keys[key] {
		return true
	}
	for _, prefix := range check.prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (plan *compiledOutputPlan) checkObjectFields(state evaluation) (plannedOutput, error) {
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	fields, err := plannedObjectFields(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	check := plan.expression.fieldCheck
	for _, field := range fields {
		if err := state.advance(); err != nil {
			return plannedOutput{}, err
		}
		if check.allows(field.key) {
			continue
		}
		path := check.diagnostic.PathPrefix + "/" + escapePointer(field.key)
		if check.isLeaf {
			value, err := field.value.Encode(state.ctx)
			if err != nil {
				return plannedOutput{}, err
			}
			path = FirstObjectLeafPath(path, value)
		}
		return plannedOutput{}, &unknownObjectFieldError{path: path, diagnostic: check.diagnostic}
	}
	return source, nil
}

type unknownObjectFieldError struct {
	path       string
	diagnostic *MergeConflictDiagnostic
}

func (failure *unknownObjectFieldError) Error() string {
	return "undeclared object field at " + failure.path
}

func (failure *unknownObjectFieldError) issue(identity Identity, direction Direction) ConversionIssue {
	diagnostic := failure.diagnostic
	return ConversionIssue{Code: diagnostic.Code, Severity: SeverityError, Protocol: identity, Direction: direction,
		Stage: diagnostic.Stage, Path: failure.path, Reason: diagnostic.Reason, Suggestion: diagnostic.Suggestion}
}

// FirstObjectLeafPath follows the lexically first member of each object, stopping
// at an empty object, array or scalar. Values never become diagnostic segments.
func FirstObjectLeafPath(path string, value Value) string {
	for value.IsObject() {
		fields, _ := value.ReadObject()
		if len(fields) == 0 {
			break
		}
		key := sortedKeys(fields)[0]
		path += "/" + escapePointer(key)
		value = fields[key]
	}
	return path
}
