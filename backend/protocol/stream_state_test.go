package protocol

import (
	"strings"
	"testing"
)

func TestUsageSnapshotsDistinguishPresenceAndObservedZero(t *testing.T) {
	observed := func(count int64) *Counter { return &Counter{Count: count, Origin: ObservedCount} }
	before := &Usage{Input: observed(80), Output: observed(10), CacheRead: observed(50), Total: observed(90)}
	after := MergeUsage(before, &Usage{Output: observed(0), CacheRead: observed(0), Total: &Counter{Origin: InferredCount}})
	if after.Input.Count != 80 || after.Output.Count != 0 || after.CacheRead.Count != 0 || after.Total.Count != 90 || after.Total.Origin != ObservedCount {
		t.Fatalf("partial/zero snapshot: %#v", after)
	}
	after.Input.Count = 2
	if before.Input.Count != 80 {
		t.Fatal("merge aliased input counters")
	}
	inferred := MergeUsage(&Usage{Input: observed(80)}, &Usage{Output: observed(2)})
	if inferred.Total.Count != 82 || inferred.Total.Origin != InferredCount {
		t.Fatalf("inferred total: %+v", inferred.Total)
	}
}

func TestStreamTracksLongTextWithoutRetainingPayload(t *testing.T) {
	state, err := NewStreamState(DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	delta := strings.Repeat("x", 8192)
	for range 4096 {
		if _, err := state.TrackText("text", delta, false); err != nil {
			t.Fatal(err)
		}
	}
	if state.buffered != 0 || state.texts["text"].Length() != 32<<20 {
		t.Fatal("text retained in buffers or length lost")
	}
	if _, err := state.TrackText("text", "rewrite", true); err == nil {
		t.Fatal("shortened snapshot accepted")
	}
}

func TestStreamSnapshotRejectsRewriteAndAllowsSuffix(t *testing.T) {
	var tracker TextTracker
	tracker.Append("prefix")
	if _, err := tracker.Snapshot("PREFIX"); err == nil {
		t.Fatal("rewrite accepted")
	}
	if suffix, err := tracker.Snapshot("prefix tail"); err != nil || suffix != " tail" {
		t.Fatalf("suffix=%q, err=%v", suffix, err)
	}
	if suffix, err := tracker.Snapshot("prefix tail"); err != nil || suffix != "" {
		t.Fatalf("duplicate snapshot: %q, %v", suffix, err)
	}
}

func TestStreamToolCompletionAndAssociation(t *testing.T) {
	state, _ := NewStreamState(DefaultLimits())
	if err := state.ObserveTool("json", "call-a", JSONInput); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTool("json", `{"q":`, false, false); err != nil {
		t.Fatal(err)
	}
	if err := state.ObserveTool("text", "call-b", TextInput); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTool("text", "not JSON", false, true); err != nil {
		t.Fatal(err)
	}
	if err := state.ObserveTool("duplicate", "call-a", JSONInput); err == nil {
		t.Fatal("duplicate call ID accepted")
	}
	if err := state.AppendTool("json", `{"q":1}`, true, true); err != nil {
		t.Fatal(err)
	}
	if state.buffered != 0 {
		t.Fatal("completed input not released")
	}
	if err := state.AppendTool("json", `{"q":1}`, true, true); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTool("json", `{"q":2}`, true, true); err == nil {
		t.Fatal("conflicting completion accepted")
	}
	if err := state.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := state.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.TrackText("late", "late", false); err == nil {
		t.Fatal("post-terminal content accepted")
	}
}

func TestStreamRejectsMalformedOrMissingFunctionInput(t *testing.T) {
	for _, input := range []string{"", "not JSON", `{"q":`} {
		state, _ := NewStreamState(DefaultLimits())
		_ = state.ObserveTool("tool", "call", JSONInput)
		_ = state.AppendTool("tool", input, false, false)
		if err := state.Finish(); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestStreamEnforcesBufferAndItemLimits(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes, limits.StateItems = 8, 1
	state, _ := NewStreamState(limits)
	_ = state.ObserveTool("tool", "call", JSONInput)
	if err := state.AppendTool("tool", strings.Repeat("x", 9), false, false); err == nil {
		t.Fatal("buffer overflow accepted")
	}
	if _, err := state.TrackText("extra", "x", false); err == nil {
		t.Fatal("item overflow accepted")
	}
}
