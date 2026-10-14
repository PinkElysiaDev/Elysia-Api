package protocol

import "testing"

func TestLegacyJobOutcomeProjection(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "canceled"} {
		original := GenerationJob{Phase: JobTerminal, Task: Task{Status: TaskCompleted, Result: &Response{Status: StringValue(status)}}}
		projected := classifyJobResult(original)
		expected := TaskFailed
		if status != "failed" {
			expected = TaskCancelled
		}
		if projected.Task.Status != expected || original.Task.Status != TaskCompleted || len(projected.Issues) == 0 {
			t.Fatalf("invalid projection: %+v", projected)
		}
	}
}
