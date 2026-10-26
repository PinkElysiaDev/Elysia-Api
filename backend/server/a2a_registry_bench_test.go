package server

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func BenchmarkA2ARegistry(b *testing.B) {
	b.Run("Duplicate", func(b *testing.B) {
		h := (&Server{}).a2aService()
		h.register("existing", "session", true)
		b.ReportAllocs()
		for b.Loop() {
			h.register("existing", "session", true)
		}
		b.ReportMetric(float64(reflect.TypeFor[a2aTaskEntry]().Size()), "entry-bytes")
	})
	b.Run("InsertBelowCapacity", func(b *testing.B) {
		h := (&Server{}).a2aService()
		b.ReportAllocs()
		for b.Loop() {
			h.register("new", "session", true)
			// Both versions include this private-fixture cleanup to keep the
			// registration below capacity without timing clock pauses per call.
			delete(h.tasks, "new")
		}
		b.ReportMetric(float64(reflect.TypeFor[a2aTaskEntry]().Size()), "entry-bytes")
	})
	for _, state := range []a2a.TaskState{a2a.TaskStateCompleted, a2a.TaskStateWorking} {
		b.Run("Evict/"+string(state), func(b *testing.B) {
			h := (&Server{}).a2aService()
			var nextID uint64
			register := func() {
				nextID++
				id := a2a.TaskID(strconv.FormatUint(nextID, 10))
				entry := h.register(id, "session", true)
				// No other goroutine owns this fixture. Publish its state and
				// message directly so the benchmark excludes SDK event creation.
				entry.task.Status.State = state
				h.messages[string(id)] = a2aRun{entry: entry}
			}
			for range a2aRegistryLimit {
				register()
			}
			b.ReportAllocs()
			for b.Loop() {
				register()
			}
			b.ReportMetric(float64(reflect.TypeFor[a2aTaskEntry]().Size()), "entry-bytes")
		})
	}
}
