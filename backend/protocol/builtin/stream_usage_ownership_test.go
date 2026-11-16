package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestTargetStreamUsageMatchesSnapshotMerging(t *testing.T) {
	compiled := testCompiled(t, Chat)
	shouldInclude := true
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), ClientOutput: &p.ClientOutput{IncludeUsage: &shouldInclude}})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	input := &p.Usage{Input: &p.Counter{Count: 9, Origin: p.ObservedCount}}
	events := []p.Event{
		{Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}, Usage: input}},
		{Type: p.MetadataUpdated},
		{Type: p.MetadataUpdated, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion}},
		{Type: p.UsageUpdated, Usage: &p.Usage{Output: &p.Counter{Count: 0, Origin: p.ObservedCount}}},
		{Type: p.MetadataUpdated, Usage: &p.Usage{}, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Usage: &p.Usage{CacheRead: &p.Counter{Count: 0, Origin: p.ObservedCount}}}},
		{Type: p.ResponseFinished},
		{Type: p.UsageUpdated, Usage: &p.Usage{Total: &p.Counter{Count: 9, Origin: p.ObservedCount}}},
	}
	var output []p.Value
	for index, event := range events {
		event.SchemaVersion, event.ResponseID = p.ProtocolCoreVersion, p.StringValue("r")
		frames, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(index, err)
		}
		consume(frames)
		output = append(output, frames...)
		if index == 0 {
			input.Input.Count = 999
		}
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
	output = append(output, tail...)
	count := 0
	for _, frame := range output {
		fields, _ := frame.ReadObject()
		if !fields["usage"].IsZero() {
			count++
			sameJSON(t, fields["usage"].Bytes(), `{"prompt_tokens":9,"completion_tokens":0,"total_tokens":9,"prompt_tokens_details":{"cached_tokens":0}}`)
		}
	}
	if count != 1 {
		t.Fatal("target did not emit exactly one accumulated usage snapshot", count)
	}
}
