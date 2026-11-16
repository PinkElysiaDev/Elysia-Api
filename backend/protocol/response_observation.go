package protocol

import "strconv"

const (
	metadataImageCompression     = "image-context-compression"
	metadataInputTransformations = "input-transformations"
	transformationKindReasoning  = "reasoning"
	transformationActionRemove   = "remove"
	transformationFieldCount     = 4
	transformationOrganization   = "organization-mismatch"
	transformationEndUser        = "end-user-mismatch"
	transformationModel          = "model-mismatch"
	transformationPrefix         = "prefix-mismatch"
	transformationTurnBoundary   = "turn-boundary"
)

// Observations describe completed source behavior; they confer no permission
// to repeat a transformation or resolve an identity in another source scope.
func validateResponseObservation(field ResponseMetadata) error {
	if field.Location != "response" {
		return streamIssue(InvalidInput, field.Path, "response observation requires a response owner")
	}
	if field.Value.IsNull() {
		return nil
	}
	if field.Meaning == metadataInputTransformations {
		return validateInputTransformations(field.Value, field.Path)
	}
	var isEnabled bool
	if field.Value.Decode(&isEnabled) != nil {
		return streamIssue(InvalidInput, field.Path, "image compression observation requires a boolean or null")
	}
	return nil
}

func validateInputTransformations(value Value, path string) error {
	var transformations []Value
	if value.Decode(&transformations) != nil {
		return streamIssue(InvalidInput, path, "input transformations require an ordered array or null")
	}
	for index, transformation := range transformations {
		location := path + "/" + strconv.Itoa(index)
		fields, err := transformation.ReadObject()
		if err != nil || len(fields) != transformationFieldCount {
			return streamIssue(InvalidInput, location, "input transformation requires sourceItemId, kind, action and reason")
		}
		if !fields["sourceItemId"].IsStringValue() || fields["sourceItemId"].IsString("") {
			return streamIssue(InvalidInput, location+"/sourceItemId", "source item identity requires a nonempty string")
		}
		if !fields["kind"].IsString(transformationKindReasoning) {
			return streamIssue(InvalidInput, location+"/kind", "input transformation kind must be reasoning")
		}
		if !fields["action"].IsString(transformationActionRemove) {
			return streamIssue(InvalidInput, location+"/action", "input transformation action must be remove")
		}
		if !hasTransformationReason(fields["reason"]) {
			return streamIssue(InvalidInput, location+"/reason", "input transformation reason has no declared core meaning")
		}
	}
	return nil
}

func hasTransformationReason(value Value) bool {
	var reason string
	if value.Decode(&reason) != nil {
		return false
	}
	switch reason {
	case transformationOrganization, transformationEndUser, transformationModel, transformationPrefix, transformationTurnBoundary:
		return true
	}
	return false
}
