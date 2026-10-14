package builtin

import (
	"errors"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

// Source identity checks happen before projection can substitute client IDs.
// Missing identities are handled by each wire contract, not guessed here.
func (stream *streamModule) validateSourceIdentity(fields p.Object) error {
	if failure := fields["error"]; !failure.IsZero() && !failure.IsNull() {
		return nil
	}
	var keys []string
	base := ""
	switch stream.name {
	case Chat:
		keys = []string{"id", "model", "created"}
	case Responses:
		if fields["response"].IsZero() {
			return nil
		}
		var err error
		fields, err = fields["response"].ReadObject()
		if err != nil {
			return err
		}
		keys, base = []string{"id", "model", "created_at"}, "/response"
	default:
		return nil
	}
	for index, key := range keys {
		if err := stream.sourceIdentity.bind(index, base+"/"+key, fields[key], stream.limits.BufferBytes); err != nil {
			var issue *p.ConversionError
			if !errors.As(err, &issue) {
				return err
			}
			for i := range issue.Issues {
				issue.Issues[i].Code, issue.Issues[i].Stage = p.UpstreamContractViolation, "decode.event"
				issue.Issues[i].Reason = fmt.Sprintf("provider frame %d: %s", stream.sourceFrameNumber, issue.Issues[i].Reason)
			}
			return issue
		}
	}
	return nil
}

func (stream *streamModule) rejectUnknownFailedEvent(fields p.Object, options p.EvaluationContext) error {
	response, err := fields["response"].ReadObject()
	if err != nil || !response["status"].IsString("failed") {
		return nil
	}
	contract := p.IssuesError([]p.ConversionIssue{{Code: p.UpstreamContractViolation, Severity: p.SeverityError, Stage: "decode.event", Path: "/type", Reason: fmt.Sprintf("provider frame %d: unknown event type declares a failed response; expected response.failed", stream.sourceFrameNumber)}})
	if response["error"].IsZero() || response["error"].IsNull() {
		return contract
	}
	failure, err := stream.module.decodeFailure(response["error"], options)
	if err != nil {
		return errors.Join(contract, err)
	}
	return errors.Join(&p.GenerationFailure{Payload: failure, Status: response["status"]}, contract)
}
