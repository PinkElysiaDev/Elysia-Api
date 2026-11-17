package protocol

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	sourceCapacityItems    = 64
	sourceCapacityMessages = 48
)

func TestResponsesPackageSourceCapacity(t *testing.T) {
	spec, err := os.ReadFile("builtin/packages/openai-responses/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("builtin/packages/openai-responses/mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(spec, mapping)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	// Authoring fixtures use the normal compile budget; this test isolates the
	// existing stream-retention boundary without removing any package samples.
	shared := compiled
	compiled = detachedCompiledFixture(shared)
	compiled.limits.StateItems = sourceCapacityItems
	if shared.limits.StateItems != compiler.limits.StateItems {
		t.Fatal("stream-retention fixture mutated the CompileSources cache graph")
	}
	for _, messageCount := range []int{sourceCapacityMessages, sourceCapacityItems + 1} {
		t.Run(fmt.Sprint(messageCount), func(t *testing.T) {
			checkResponsesSourceCapacity(t, compiled, messageCount)
		})
	}
}

func checkResponsesSourceCapacity(t *testing.T, compiled *Compiled, messageCount int) {
	t.Helper()
	frames := []string{`{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`}
	var output []string
	for index := range messageCount {
		message := fmt.Sprintf(`{"type":"message","id":"m_%d","role":"assistant","status":"completed","content":[]}`, index)
		frames = append(frames,
			fmt.Sprintf(`{"type":"response.output_item.added","output_index":%d,"item":%s}`, index, strings.Replace(message, "completed", "in_progress", 1)),
			fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, message))
		output = append(output, message)
	}
	frames = append(frames, `{"type":"response.completed","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[`+strings.Join(output, ",")+`]}}`)
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	finishedMessages, finishedResponses := 0, 0
	for index, raw := range frames {
		frame, err := validation.DecodeFrame(t.Context(), parseTestValue(t, raw), options)
		if err != nil {
			var limit *ResourceLimitError
			if messageCount > sourceCapacityItems && errors.As(err, &limit) && limit.Domain == "stream" && limit.Metric == "state_items" && limit.Limit == sourceCapacityItems && limit.Observed > sourceCapacityItems {
				return
			}
			t.Fatal("source capacity decreased", index, err)
		}
		for _, event := range frame.Events {
			if event.Type == ItemFinished {
				finishedMessages++
			}
			if event.Type == ResponseFinished {
				finishedResponses++
				if event.Response == nil || len(event.Response.Content) != messageCount {
					t.Fatal("terminal output lost registered messages")
				}
			}
		}
	}
	if messageCount > sourceCapacityItems {
		t.Fatal("stream exceeded its retained-item budget")
	}
	if finishedMessages != messageCount || finishedResponses != 1 {
		t.Fatal("messages or response completed more than once", finishedMessages, finishedResponses)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
}
