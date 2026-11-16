package protocol

import (
	"reflect"
	"testing"
)

func TestPrivateStreamUsageMatchesSnapshotMerging(t *testing.T) {
	for _, owner := range []string{"replay"} {
		t.Run(owner, func(t *testing.T) {
			input := &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}}
			events := []Event{
				{Type: ResponseStarted, Response: &Response{SchemaVersion: SemanticSchemaVersion, Usage: input}},
				{Type: MetadataUpdated},
				{Type: MetadataUpdated, Response: &Response{SchemaVersion: SemanticSchemaVersion}},
				{Type: UsageUpdated, Usage: &Usage{Output: &Counter{Count: 0, Origin: ObservedCount}, CacheRead: &Counter{Count: 0, Origin: ObservedCount}}},
				{Type: MetadataUpdated, Usage: &Usage{}, Response: &Response{SchemaVersion: SemanticSchemaVersion, Usage: &Usage{Details: map[string]Counter{"output.reasoning_tokens": {Count: 0, Origin: ObservedCount}}}}},
				{Type: ResponseFinished},
				{Type: UsageUpdated, Usage: &Usage{Total: &Counter{Count: 7, Origin: ObservedCount}, CacheCreation: &Counter{Count: 0, Origin: ObservedCount}}},
			}
			replay, err := NewEventReplay(replayTarget(), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			var expected, actual *Usage
			for index, event := range events {
				event.SchemaVersion = SemanticSchemaVersion
				before := actual
				// This is the original unconditional merge order, including nil
				// updates and an independently supplied response snapshot.
				expected = MergeUsage(expected, event.Usage)
				if event.Response != nil {
					expected = MergeUsage(expected, event.Response.Usage)
				}
				_, err = replay.Consume(event)
				actual = replay.usage
				if err != nil || !reflect.DeepEqual(actual, expected) {
					t.Fatalf("event %d: usage=%+v, want %+v, error=%v", index, actual, expected, err)
				}
				if event.Usage == nil && (event.Response == nil || event.Response.Usage == nil) && actual != before {
					t.Fatalf("event %d cloned an unchanged private usage snapshot", index)
				}
			}
			input.Input.Count = 999
			if actual.Input.Count != 7 {
				t.Fatal("private usage aliases the caller's counters")
			}
			if owner == "replay" {
				if err := replay.Finish(); err != nil {
					t.Fatal(err)
				}
				snapshot := replay.Usage()
				snapshot.Input.Count = 123
				if replay.Usage().Input.Count != 7 {
					t.Fatal("published usage aliases private state")
				}
			}
		})
	}
}
