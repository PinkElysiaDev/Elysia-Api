package protocol

import "slices"

const (
	outputConstraintPath    = "/parameters/outputConstraint"
	outputConstraintVersion = 1
)

// The core owns intent shape. Each target Mapping owns schema expressibility.
func validateOutputConstraint(value Value) error {
	if value.IsZero() {
		return nil
	}
	fields, err := value.readObjectWithin(&scalarInputReader)
	if err != nil {
		return streamIssue(InvalidInput, outputConstraintPath, "output constraint must be an object")
	}
	allowed := []string{"version", "kind", "enforcement", "dialect", "schema", "name", "description"}
	for _, name := range sortedKeys(fields) {
		if !slices.Contains(allowed, name) {
			return streamIssue(InvalidInput, outputConstraintPath+"/"+escapePointer(name), "unknown output constraint member")
		}
	}
	var version int
	if fields["version"].Decode(&version) != nil || version != outputConstraintVersion {
		return streamIssue(InvalidInput, outputConstraintPath+"/version", "output constraint version must be 1")
	}
	kind := fields["kind"]
	if !kind.IsString("text") && !kind.IsString("json") && !kind.IsString("json-object") && !kind.IsString("json-schema") {
		return streamIssue(InvalidInput, outputConstraintPath+"/kind", "output constraint kind must be text, json, json-object or json-schema")
	}
	enforcement := fields["enforcement"]
	if !enforcement.IsString("required") && !enforcement.IsString("preferred") {
		return streamIssue(InvalidInput, outputConstraintPath+"/enforcement", "output constraint enforcement must be required or preferred")
	}
	if !kind.IsString("json-schema") {
		for _, name := range []string{"dialect", "schema", "name", "description"} {
			if !fields[name].IsZero() {
				return streamIssue(InvalidInput, outputConstraintPath+"/"+name, "this member requires a JSON Schema output constraint")
			}
		}
		if !enforcement.IsString("required") {
			return streamIssue(InvalidInput, outputConstraintPath+"/enforcement", "text and JSON-only formats require an explicit output contract")
		}
		return nil
	}
	if !fields["dialect"].IsString("json-schema") {
		return streamIssue(InvalidInput, outputConstraintPath+"/dialect", "output constraint dialect must be json-schema")
	}
	if !fields["schema"].IsObject() {
		return streamIssue(InvalidInput, outputConstraintPath+"/schema", "output constraint schema must be an object")
	}
	for _, name := range []string{"name", "description"} {
		if !fields[name].IsZero() && jsonValueKind(fields[name]) != "string" {
			return streamIssue(InvalidInput, outputConstraintPath+"/"+name, "output constraint hint must be a string")
		}
	}
	return nil
}
