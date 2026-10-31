//go:build coreplan_bootstrap

package protocol

// Generation excludes stale direct field references before importing the model.
// These entrypoints are never executed by the generator or a production build.
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
func decodeCoreResponseOutput(JSONOutput) (*Response, error)          { panic("coreplan generation") }
func decodeCoreEventOutput(JSONOutput) (*Event, error)                { panic("coreplan generation") }
func decodeCoreEventsOutput(JSONOutput) ([]Event, error)              { panic("coreplan generation") }
func readCoreRequestOutput(*Request, []string) (JSONOutput, error)    { panic("coreplan generation") }
func readCoreResponseOutput(*Response, []string) (JSONOutput, error)  { panic("coreplan generation") }
func readCoreEventOutput(*Event, []string) (JSONOutput, error)        { panic("coreplan generation") }
func readCoreEventsOutput([]Event, []string) (JSONOutput, error)      { panic("coreplan generation") }
func readCoreCacheIntent(CacheIntent, []string) (JSONOutput, error)   { panic("coreplan generation") }
func readCoreNode(Node, []string) (JSONOutput, error)                 { panic("coreplan generation") }
func readCoreProvenance(Provenance, []string) (JSONOutput, error)     { panic("coreplan generation") }
func readCoreRequest(Request, []string) (JSONOutput, error)           { panic("coreplan generation") }
func readCoreClientOutput(ClientOutput, []string) (JSONOutput, error) { panic("coreplan generation") }
func readCoreUsage(Usage, []string) (JSONOutput, error)               { panic("coreplan generation") }
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
