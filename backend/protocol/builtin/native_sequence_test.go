package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesNativeReplayFailureSequence(t *testing.T) {
	for _, makeProtocol := range []struct {
		name string
		make func(*testing.T, string) *p.Compiled
	}{{"preset", shippedProjectionProtocol}, {"custom-module-id", func(t *testing.T, name string) *p.Compiled { return testCompiled(t, name) }}} {
		t.Run(makeProtocol.name, func(t *testing.T) {
			c := makeProtocol.make(t, Responses)
			for _, terminal := range []p.EventType{p.OperationFailed, p.OperationCancelled} {
				t.Run(string(terminal), func(t *testing.T) {
					options := p.EvaluationContext{State: p.NewEvaluationState()}
					validator, err := c.NewWireStreamValidation(p.Scope{})
					if err != nil {
						t.Fatal(err)
					}
					// Nonzero first sequence and a gap must not be replaced by frame counts.
					for _, body := range []string{
						`{"type":"response.created","sequence_number":41,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[]}}`,
						`{"type":"response.in_progress","sequence_number":49,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[]}}`,
					} {
						frame, err := c.DecodeFrame(t.Context(), testValue(t, body), options)
						if err != nil {
							t.Fatal(err)
						}
						wire, err := c.EncodeFrame(t.Context(), frame, options)
						if err != nil || len(wire) != 1 || string(wire[0].Bytes()) != body {
							t.Fatal(wire, err)
						}
						if err := validator.Consume(t.Context(), wire[0]); err != nil {
							t.Fatal(err)
						}
					}
					failure := p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: terminal, Error: testValue(t, `{"category":"upstream","message":"synthetic local failure"}`)}
					wire, err := c.EncodeFrames(t.Context(), failure, options)
					if err != nil || len(wire) != 1 {
						t.Fatal(wire, err)
					}
					fields, _ := wire[0].ReadObject()
					if fields["sequence_number"] != testValue(t, `50`) {
						t.Fatalf("native sequence was reset: %s", wire[0].Bytes())
					}
					if err := validator.Consume(t.Context(), wire[0]); err != nil {
						t.Fatal(err)
					}
					if err := validator.Finish(); err != nil {
						t.Fatal(err)
					}
					if tail, err := c.FinishEvents(t.Context(), options); err != nil || len(tail) != 0 {
						t.Fatal(tail, err)
					}
					// A new request owns a new sequence; neither the decoder nor another
					// request can share the encoder's native watermark.
					fresh, err := c.EncodeFrames(t.Context(), failure, p.EvaluationContext{State: p.NewEvaluationState()})
					if err != nil || len(fresh) != 1 {
						t.Fatal(fresh, err)
					}
					f, _ := fresh[0].ReadObject()
					if f["sequence_number"] != testValue(t, `0`) {
						t.Fatal(string(fresh[0].Bytes()))
					}
				})
			}
		})
	}
}

func TestResponsesNativeCompletionDoesNotGenerateAnotherTerminal(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	for i, status := range []string{"in_progress", "completed"} {
		kind := "response.created"
		if i == 1 {
			kind = "response.completed"
		}
		body := fmt.Sprintf(`{"type":%q,"sequence_number":%d,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":%q,"output":[]}}`, kind, i+90, status)
		frame, err := c.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := c.EncodeFrame(t.Context(), frame, options)
		if err != nil || len(wire) != 1 || string(wire[0].Bytes()) != body {
			t.Fatal(wire, err)
		}
	}
	if tail, err := c.FinishEvents(t.Context(), options); err != nil || len(tail) != 0 {
		t.Fatal(tail, err)
	}
}

func TestResponsesNativeSequenceCannotOverflow(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	frame, err := c.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","sequence_number":9223372036854775807,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[]}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.EncodeFrame(t.Context(), frame, options); err != nil {
		t.Fatal(err)
	}
	_, err = c.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: p.OperationFailed, Error: testValue(t, `{"message":"failed"}`)}, options)
	if err == nil || !strings.Contains(err.Error(), "/sequence_number") {
		t.Fatal("overflow must not reset/wrap sequence", err)
	}
}
