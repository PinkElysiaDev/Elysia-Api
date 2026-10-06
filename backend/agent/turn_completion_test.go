package agent

import (
	"testing"
)

func TestTurnEventStreamClosesAfterCleanup(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "done"}}}}
	engine := newTestEngine(caller, store)
	ctx, handle, cancel, err := engine.begin("s1", engine.opts.TurnTimeout)
	if err != nil {
		t.Fatal(err)
	}
	cancelStarted, releaseCancel := make(chan struct{}), make(chan struct{})
	events := engine.spawnTurn(ctx, "s1", handle, func() {
		cancel()
		close(cancelStarted)
		<-releaseCancel
	}, &UserContent{Text: "hi"}, nil, ApprovalDecision{})
	<-cancelStarted
	// The callback is a deterministic point inside deferred cleanup. Consumers
	// must not observe EOF while this turn still owns the session guard.
	isClosed := false
drain:
	for {
		select {
		case _, isOpen := <-events:
			if !isOpen {
				isClosed = true
				break drain
			}
		default:
			break drain
		}
	}
	close(releaseCancel)
	collectEvents(t, events)
	if isClosed {
		t.Fatal("event stream closed before deferred cleanup completed")
	}
	if engine.IsRunning("s1") {
		t.Fatal("completed stream retained the session guard")
	}
}
