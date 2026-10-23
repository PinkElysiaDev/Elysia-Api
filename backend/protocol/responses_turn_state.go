package protocol

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/bytedance/sonic/ast"
)

// ResponsesTurnStatePath identifies opaque control state without exposing it.
const ResponsesTurnStatePath = "/headers/x-codex-turn-state"

// ResponsesTurnState is opaque control data owned by one upstream frame. It is
// never generation content or a continuation token that the gateway can restore.
type ResponsesTurnState struct {
	Frame       Value      `json:"frame"`
	Source      Provenance `json:"source"`
	FrameNumber int        `json:"frameNumber"`
}

// ValidateResponsesTurnStateFrame recognizes only the observed control shape.
// Unknown payloads retain an explicit rejection boundary instead of being lost
// with the known header during cross-protocol projection.
func ValidateResponsesTurnStateFrame(frame Value) error {
	fields, err := readControlFields(frame, "")
	if err != nil || !fields["type"].IsString("response.metadata") {
		return streamIssue(InvalidInput, "/type", "turn state requires response.metadata")
	}
	for _, key := range sortedKeys(fields) {
		if key != "type" && key != "headers" && key != "sequence_number" {
			return streamIssue(UnsupportedCapability, "/"+escapePointer(key), "unrecognized turn-state control payload requires an explicit mapping")
		}
	}
	headers, err := readControlFields(fields["headers"], "/headers")
	if err != nil || len(headers) != 1 {
		return streamIssue(InvalidInput, "/headers", "turn-state control requires exactly one header without duplicate case aliases")
	}
	for key, value := range headers {
		if !strings.EqualFold(key, "x-codex-turn-state") {
			return streamIssue(UnsupportedCapability, "/headers/"+escapePointer(key), "unrecognized control header requires an explicit mapping")
		}
		var state string
		if value.IsNull() || value.Decode(&state) != nil || state == "" || strings.IndexFunc(state, unicode.IsControl) >= 0 {
			return streamIssue(InvalidInput, ResponsesTurnStatePath, "turn state must be a nonempty string without control characters")
		}
	}
	if sequence := fields["sequence_number"]; !sequence.IsZero() {
		var number int64
		if sequence.IsNull() || sequence.Decode(&number) != nil || number < 0 {
			return streamIssue(InvalidInput, "/sequence_number", "control sequence must be a nonnegative integer when present")
		}
	}
	return nil
}

// Duplicate keys would make raw replay ambiguous even if a map kept the last
// value. Header aliases are compared case-insensitively before map decoding.
func readControlFields(value Value, path string) (Object, error) {
	if !value.IsObject() {
		return nil, streamIssue(InvalidInput, path, "control fields require an object")
	}
	node := ast.NewRaw(value.raw)
	properties, err := node.Properties()
	if err != nil {
		return nil, streamIssue(InvalidInput, path, "invalid control object")
	}
	fields, seen := Object{}, map[string]bool{}
	var property ast.Pair
	for properties.Next(&property) {
		name := property.Key
		canonical := name
		if path == "/headers" {
			canonical = strings.ToLower(name)
		}
		if seen[canonical] {
			return nil, streamIssue(InvalidInput, path, "duplicate control field")
		}
		seen[canonical] = true
		raw, err := property.Value.Raw()
		if err != nil {
			return nil, streamIssue(InvalidInput, path, "invalid control value")
		}
		fields[name] = Value{raw: strings.Clone(raw)}
	}
	if node.Check() != nil {
		return nil, streamIssue(InvalidInput, path, "invalid control object")
	}
	return fields, nil
}

func (c *CompiledConversion) projectResponsesTurnState(value Value, rule ConversionRule, sink *DiagnosticSink, limits Limits) (Value, error) {
	var event Event
	if err := decodeContract(value.Bytes(), &event); err != nil {
		return Value{}, err
	}
	shouldEncode, err := c.projectTurnStateObject(&event, rule, sink)
	if err != nil {
		return Value{}, err
	}
	if !shouldEncode {
		return value, nil
	}
	return encodeSemanticWithin(&event, limits)
}

func (c *CompiledConversion) projectTurnStateObject(event *Event, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	state := event.ResponsesTurnState
	if state == nil {
		return false, nil
	}
	if err := ValidateResponsesTurnStateFrame(state.Frame); err != nil {
		return false, err
	}
	if rule.Value.IsString("responses") {
		return false, nil
	}
	issue := ConversionIssue{
		Code: ConversionDegraded, Severity: SeverityWarning, Fidelity: "lossy_compatible",
		Protocol: state.Source.Protocol, Direction: DecodeEvent, Stage: "conversion.event", Path: ResponsesTurnStatePath,
		RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID],
		Reason: fmt.Sprintf("provider frame %d: target has no turn-state control channel; omitted state does not preserve stateful continuation", state.FrameNumber),
	}
	if c.Policy.Mode == "strict" {
		issue.Code, issue.Severity, issue.Fidelity = ConversionRejected, SeverityError, "rejected"
		return false, IssuesError([]ConversionIssue{issue})
	}
	sink.Add(issue)
	event.ResponsesTurnState, event.Native = nil, nil
	return true, nil
}
