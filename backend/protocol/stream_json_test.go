package protocol

import "testing"

func TestJSONToolSnapshotsOnlyNormalizeCompleteEquivalentTokens(t *testing.T) {
	for _, tc := range []struct {
		previous, snapshot string
		ok                 bool
	}{
		{`{ "n": 9007199254740993, "text": "a&b" }`, `{"n":9007199254740993,"text":"a\u0026b"}`, true},
		{`{ "n": 9007199254740993 }`, `{"n":9007199254740992}`, false},
		{`{"n":1}`, `{"n":"1"}`, false},
		{`{"n":1}`, `"{\"n\":1}"`, false},
		{`{ "n":1, "n":2 }`, `{"n":2}`, false},
		{`{ "n":`, `{"n":1}`, false},
		{`{"n":`, `{"n":1}`, true},
		{`not JSON`, `not  JSON`, false},
	} {
		_, err := JSONToolSnapshotSuffix(tc.previous, tc.snapshot)
		if (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	state := DefaultStreamState()
	if err := state.ObserveTool("tool", "call", JSONInput); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTool("tool", `{ "n": 9007199254740993 }`, false, false); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTool("tool", `{"n":9007199254740993}`, true, true); err != nil {
		t.Fatal(err)
	}
	tracker := &TextTracker{}
	tracker.Append(`{ "n":1 }`)
	if _, err := tracker.Snapshot(`{"n":1}`); err == nil {
		t.Fatal("text snapshot guard weakened")
	}
}
