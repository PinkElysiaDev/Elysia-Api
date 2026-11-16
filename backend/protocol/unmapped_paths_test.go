package protocol

import "testing"

func TestUnmappedPathsUseSourceDeclarations(t *testing.T) {
	source := Identity{Family: "source", DefinitionID: "source", Revision: "1"}
	target := Target{Protocol: Identity{Family: "target", DefinitionID: "target", Revision: "1"}, Direction: EncodeEvent, Capabilities: CapabilitySet{NativeExtensionsCapability: true}}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted,
		Unmapped:      &Native{Source: Provenance{Protocol: source, Direction: DecodeEvent}, Value: parseTestValue(t, `{"extra":1,"nested":{"a/b":2}}`)},
		UnmappedPaths: []string{"/nested/a~1b", "/extra"}}
	issues := CheckEvent(event, target, DefaultLimits())
	if len(issues) != 2 || issues[0].Path != "/extra" || issues[1].Path != "/nested/a~1b" {
		t.Fatal("source field diagnostics changed", issues)
	}
	event.UnmappedPaths = nil
	event.Unmapped.Source.Path = "/frame/extensions"
	event.Unmapped.Value = parseTestValue(t, `{"/fake":{"legacy":true}}`)
	issues = CheckEvent(event, target, DefaultLimits())
	if len(issues) != 1 || issues[0].Path != "/unmapped" {
		t.Fatal("core interpreted a retired wire extension layout", issues)
	}
	for _, paths := range [][]string{{"not-a-pointer"}, {"/broken~"}, {""}} {
		event.UnmappedPaths = paths
		issues = CheckEvent(event, target, DefaultLimits())
		if len(issues) != 1 || issues[0].Code != InvalidInput || issues[0].Path != "/unmappedPaths/0" {
			t.Fatal("invalid diagnostic path accepted", paths, issues)
		}
	}
	event.Unmapped = nil
	event.UnmappedPaths = []string{"/extra"}
	if issues = CheckEvent(event, target, DefaultLimits()); len(issues) != 1 || issues[0].Path != "/unmappedPaths" {
		t.Fatal("source paths survived without their opaque owner", issues)
	}
}
