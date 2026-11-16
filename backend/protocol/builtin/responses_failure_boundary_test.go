package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesFailureRejectsProgress(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, failure := range []string{
		`{"type":"error","sequence_number":1,"message":"failed"}`,
		`{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"message":"failed"}}}`,
	} {
		for _, hasStarted := range []bool{false, true} {
			for _, progress := range []string{`{}`, `{"usage":{"input_tokens":1}}`, `{"metadata":{"label":"late"}}`} {
				t.Run(fmt.Sprintf("%s/%t/%s", failure, hasStarted, progress), func(t *testing.T) {
					validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
					if err != nil {
						t.Fatal(err)
					}
					defer validation.Release()
					if hasStarted {
						if err := validation.Consume(t.Context(), testValue(t, progressStart)); err != nil {
							t.Fatal(err)
						}
					}
					if err := validation.Consume(t.Context(), testValue(t, failure)); err != nil {
						t.Fatal(err)
					}
					if err := validation.Finish(); err != nil {
						t.Fatal("provider failure lost its terminal", err)
					}
					frame := testValue(t, `{"type":"response.in_progress","sequence_number":2,"response":`+progress+`}`)
					issue := requireResponsesWireRejection(t, compiled, validation.Consume(t.Context(), frame))
					if issue.Path != "/type" || validation.Finish() == nil {
						t.Fatal("progress crossed a failed stream boundary", issue)
					}
				})
			}
		}
	}
}
