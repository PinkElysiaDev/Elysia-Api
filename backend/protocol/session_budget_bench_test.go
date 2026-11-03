package protocol

import (
	"bytes"
	"testing"
)

// Both revisions run the same complete duplex trace, including final snapshots
// and release. Transport I/O and protocol compilation are outside this benchmark.
func BenchmarkPackageSessionBudget(b *testing.B) {
	for _, name := range []string{"session-alpha", "session-beta"} {
		b.Run(name, func(b *testing.B) {
			_, document, compiled := loadTransportPackage(b, "sessions", name, featureWebSocketTransport)
			operation, _ := compiled.Operation("session")
			binding := Binding{Capabilities: compiled.capabilities}
			options := sessionTestContext(b, Scope{Model: "m"})
			model := StringValue("m")
			steps := document.Mapping.SessionSamples[0].Steps
			frames := make([]SessionFrame, len(steps))
			for index, step := range steps {
				frames[index] = SessionFrame{Payload: step.Input.Bytes()}
			}
			b.ReportAllocs()
			for b.Loop() {
				adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, options, model)
				if err != nil {
					b.Fatal(err)
				}
				for index, step := range steps {
					output, err := adapter.Convert(b.Context(), eventOrigin(step.Direction), frames[index])
					if err != nil || len(output) != 1 || !bytes.Equal(output[0].Payload, frames[index].Payload) {
						b.Fatal("incomplete or changed duplex trace", err)
					}
				}
				if err := adapter.Finish(); err != nil || !adapter.IsClosed() {
					b.Fatal("session did not finish", err)
				}
				usage := adapter.ResponseUsage()
				if len(usage) != 2 || usage["r"] != nil || usage["r2"].CacheRead.Count != 40 || usage["r2"].CacheCreation.Count != 0 {
					b.Fatal("session settlement changed", usage)
				}
				adapter.Release()
			}
		})
	}
}
