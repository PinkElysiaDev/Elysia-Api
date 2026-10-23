package builtin

import (
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestTargetStreamUsageMatchesSnapshotMerging(t *testing.T) {
	instance, err := (module{name: Chat}).NewStream(p.EncodeEvent, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	stream := instance.(*streamModule)
	input := &p.Usage{Input: &p.Counter{Count: 9, Origin: p.ObservedCount}}
	events := []p.Event{
		{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Usage: input}},
		{Type: p.MetadataUpdated},
		{Type: p.MetadataUpdated, Response: &p.Response{}},
		{Type: p.UsageUpdated, Usage: &p.Usage{Output: &p.Counter{Count: 0, Origin: p.ObservedCount}}},
		{Type: p.MetadataUpdated, Usage: &p.Usage{}, Response: &p.Response{Usage: &p.Usage{CacheRead: &p.Counter{Count: 0, Origin: p.ObservedCount}}}},
		{Type: p.ResponseFinished},
		{Type: p.UsageUpdated, Usage: &p.Usage{Total: &p.Counter{Count: 9, Origin: p.ObservedCount}}},
	}
	var expected *p.Usage
	for index, event := range events {
		before := stream.usage
		expected = p.MergeUsage(expected, event.Usage)
		if event.Response != nil {
			expected = p.MergeUsage(expected, event.Response.Usage)
		}
		if _, err := stream.encodeEvent(event, p.EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stream.usage, expected) {
			t.Fatalf("event %d: usage=%+v, want %+v", index, stream.usage, expected)
		}
		if event.Usage == nil && (event.Response == nil || event.Response.Usage == nil) && stream.usage != before {
			t.Fatalf("event %d cloned an unchanged target usage snapshot", index)
		}
	}
	input.Input.Count = 999
	if stream.usage.Input.Count != 9 {
		t.Fatal("target usage aliases caller counters")
	}
}
