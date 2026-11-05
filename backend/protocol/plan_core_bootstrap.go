//go:build coreplan_bootstrap

package protocol

import "reflect"

func cloneCoreSemantic(any) reflect.Value                 { panic("coreplan generation") }
func normalizeCoreSemantic(reflect.Value, *semanticCache) { panic("coreplan generation") }

// Generation excludes stale direct field references before importing the model.
// These entrypoints are never executed by the generator or a production build.
func coreRequestFieldIndex(string) uint8 { panic("coreplan generation") }
func readCoreRequestField(*Request, uint8, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreObject(Object, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func decodeCoreRequestOutput(JSONOutput) (*Request, error) { panic("coreplan generation") }
func decodeCoreRequestOutputWithin(JSONOutput, *valueReader) (*Request, error) {
	panic("coreplan generation")
}
func decodeCoreResponseOutputWithin(JSONOutput, *valueReader) (*Response, error) {
	panic("coreplan generation")
}
func decodeCoreEventOutputWithin(JSONOutput, *valueReader) (*Event, error) {
	panic("coreplan generation")
}
func decodeCoreResponseOutput(JSONOutput) (*Response, error) { panic("coreplan generation") }
func decodeCoreEventOutput(JSONOutput) (*Event, error)       { panic("coreplan generation") }
func decodeCoreEventsOutput(JSONOutput) ([]Event, error)     { panic("coreplan generation") }
func readCoreRequestOutput(*Request, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreResponseOutput(*Response, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreEventOutput(*Event, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreEventsOutput([]Event, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreCacheIntent(CacheIntent, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreNode(Node, []string, *semanticCache) (JSONOutput, error) { panic("coreplan generation") }
func readCoreTool(Tool, []string, *semanticCache) (JSONOutput, error) { panic("coreplan generation") }
func readCoreProvenance(Provenance, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreRequest(Request, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreClientOutput(ClientOutput, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func readCoreUsage(Usage, []string, *semanticCache) (JSONOutput, error) { panic("coreplan generation") }
func writeCoreReasoningIntentPointer(JSONOutput, **ReasoningIntent, *valueReader) error {
	panic("coreplan generation")
}
func writeCoreUsagePointer(JSONOutput, **Usage, *valueReader) error { panic("coreplan generation") }
func isCoreRequestPointerObjectPath([]string) bool                  { panic("coreplan generation") }
func selectCoreRequestPointerObject(*Request, []string) (Object, bool, error) {
	panic("coreplan generation")
}
func writeCoreCacheIntentField(jsonOutputField, *CacheIntent, *valueReader) error {
	panic("coreplan generation")
}
func writeCoreEvent(JSONOutput, *Event, *valueReader) error           { panic("coreplan generation") }
func writeCoreNative(JSONOutput, *Native, *valueReader) error         { panic("coreplan generation") }
func writeCoreProvenance(JSONOutput, *Provenance, *valueReader) error { panic("coreplan generation") }
func readCoreResponseMetadata(ResponseMetadata, []string, *semanticCache) (JSONOutput, error) {
	panic("coreplan generation")
}
func writeCoreCounter(JSONOutput, *Counter, *valueReader) error         { panic("coreplan generation") }
func writeCoreCounterPointer(JSONOutput, **Counter, *valueReader) error { panic("coreplan generation") }
func writeCoreBool(JSONOutput, *bool, *valueReader) error               { panic("coreplan generation") }
func writeCoreBoolPointer(JSONOutput, **bool, *valueReader) error       { panic("coreplan generation") }
func writeCoreResponsesStorageIntentPointer(JSONOutput, **ResponsesStorageIntent, *valueReader) error {
	panic("coreplan generation")
}
