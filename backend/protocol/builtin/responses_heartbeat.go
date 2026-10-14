package builtin

import p "github.com/elysia-api/backend/protocol"

const responsesHeartbeatSentinel = "SSE-Keep-Alive"

func (stream *streamModule) consumeResponsesHeartbeat(fields p.Object, options p.EvaluationContext) (bool, error) {
	if fields[responsesHeartbeatSentinel].IsZero() {
		return false, nil
	}
	reject := func(path string) (bool, error) {
		return false, p.IssuesError([]p.ConversionIssue{{Code: p.UpstreamContractViolation, Severity: p.SeverityError, Stage: "decode.event", Path: path, Reason: "Responses transport heartbeat must contain only the declared empty sentinel delta"}})
	}
	var isKeepAlive bool
	if fields[responsesHeartbeatSentinel].Decode(&isKeepAlive) != nil || !isKeepAlive || !fields["type"].IsString("response.output_text.delta") || !fields["item_id"].IsString(responsesHeartbeatSentinel) || !fields["delta"].IsString("") {
		return reject("/" + responsesHeartbeatSentinel)
	}
	for _, key := range []string{"output_index", "content_index"} {
		var index int
		if fields[key].IsZero() || fields[key].IsNull() || fields[key].Decode(&index) != nil || index != 0 {
			return reject("/" + key)
		}
	}
	if extra := collectUnknown(fields, []string{"type", responsesHeartbeatSentinel, "item_id", "output_index", "content_index", "delta", "sequence_number"}); !extra.IsZero() {
		return reject("/" + responsesHeartbeatSentinel)
	}
	if err := stream.observeResponsesSequence(fields["sequence_number"]); err != nil {
		return false, err
	}
	options.ConsumeTransportControl()
	options.Diagnostics.Add(p.ConversionIssue{Code: p.ConversionNormalized, Severity: p.SeverityInfo, Fidelity: "preserved", Protocol: options.Identity(), Direction: p.DecodeEvent, Stage: "decode.event", Path: "/" + responsesHeartbeatSentinel, RuleID: "responses-transport-heartbeat", Evidence: "builtin-codec", Reason: "payload-free transport heartbeat consumed without opening a generation item"})
	return true, nil
}
