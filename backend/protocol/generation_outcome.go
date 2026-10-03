package protocol

import "fmt"

// GenerationFailure preserves a provider's declared failure independently of
// HTTP status. A syntactically valid HTTP 200 response may contain this result.
type GenerationFailure struct {
	Payload Value
	Status  Value
}

func (failure *GenerationFailure) Error() string {
	if !failure.Payload.IsZero() && !failure.Payload.IsNull() {
		return "upstream generation failed: " + string(failure.Payload.Bytes())
	}
	return fmt.Sprintf("upstream generation ended with status %s", failure.Status.Bytes())
}

// CheckGenerationOutcome is for execution consumers. Definition verification
// can still round-trip error responses as valid, deliberately failing outcomes.
func CheckGenerationOutcome(response *Response) error {
	if (!response.Error.IsZero() && !response.Error.IsNull()) || response.Status == StringValue("failed") || response.Status == StringValue("cancelled") || response.Status == StringValue("canceled") {
		return &GenerationFailure{Payload: response.Error, Status: response.Status}
	}
	return nil
}
