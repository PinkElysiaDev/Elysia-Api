package protocol

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPackagesPreserveExistingTraces(t *testing.T) {
	var packages []*Compiled
	for _, name := range []string{"session-alpha", "session-beta"} {
		t.Run(name, func(t *testing.T) {
			compiler, document, compiled := loadTransportPackage(t, "sessions", name, featureWebSocketTransport)
			legacyRaw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var definition Definition
			if err := decodeContract(legacyRaw, &definition); err != nil {
				t.Fatal(err)
			}
			legacy := compileSessionDefinition(t, definition)
			for _, sample := range definition.SessionSamples {
				if _, err := executeSessionSample(t.Context(), legacy, sample); err != nil {
					t.Fatal("legacy trace is not a valid reference", err)
				}
			}
			if report := Verify(t.Context(), compiled); !report.Passed {
				t.Fatal(report.Issues)
			}
			service := &Service{compiler: compiler}
			preview := service.PreviewWorkflow(t.Context(), PreviewInput{Definition: fixtureValue(t, document), Mode: "session", Sample: document.Mapping.SessionSamples[0].ID})
			if IssuesError(preview.Issues) != nil || preview.Output.IsZero() {
				t.Fatal("management preview could not find the declared session trace", preview.Issues)
			}
			binding := Binding{ProtocolID: compiled.identity.DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.capabilities, Transports: []Transport{WebSocket}}
			operation, _ := compiled.Operation("session")
			adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, sessionTestContext(t, Scope{Model: "m"}), StringValue("m"))
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range document.Mapping.SessionSamples[0].Steps {
				frames, err := adapter.Convert(t.Context(), eventOrigin(step.Direction), SessionFrame{Payload: step.Input.Bytes()})
				if err != nil || len(frames) != 1 || !bytes.Equal(frames[0].Payload, step.Input.Bytes()) {
					t.Fatal("session native frame changed", step.Direction, frames, err)
				}
			}
			if err := adapter.Finish(); err != nil || !adapter.IsClosed() || adapter.HasFailed() {
				t.Fatal("explicit close changed", err)
			}
			if _, err := adapter.EncodeFailure(t.Context(), errors.New("synthetic delivery failure")); err != nil {
				t.Fatal("package failure uses a different core version", err)
			}
			usage := adapter.ResponseUsage()
			if usage["r"] != nil || usage["r2"].CacheRead.Count != 40 || usage["r2"].CacheCreation.Count != 0 {
				t.Fatal("session usage changed", usage)
			}
			packages = append(packages, compiled)
		})
	}
	if len(packages) == 2 {
		if report := VerifyCombination(t.Context(), packages[0], packages[1]); !report.Passed {
			t.Fatal("two declared session protocols no longer compose", report.Issues)
		}
	}
}

func TestSessionPackageUsesExistingMediaAndModelBoundary(t *testing.T) {
	compiler, document, _ := loadTransportPackage(t, "sessions", "session-alpha", featureWebSocketTransport)
	document.Mapping.Capabilities[RealtimeMediaCapability] = true
	for _, direction := range []Direction{DecodeClientEvent, EncodeUpstreamEvent, DecodeEvent, EncodeEvent} {
		document.Mapping.Directions[direction].Capabilities[RealtimeMediaCapability] = true
	}
	session := document.Spec.Operations["session"].Session
	session.InputMedia, session.OutputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}, &MediaFormat{Type: "audio", Format: "pcm16"}
	compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	checkSessionAdapterMediaAndModel(t, compiled)
}

func TestSessionPackageRequiresCompleteLanesAndEvidence(t *testing.T) {
	compiler, original, verified := loadTransportPackage(t, "sessions", "session-alpha", featureWebSocketTransport)
	for _, fixture := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"missing-document", func(p *ProtocolPackage) { p.Spec.Operations["session"].Session.ClientEvent = "missing" }},
		{"missing-lane", func(p *ProtocolPackage) {
			delete(p.Mapping.Directions, DecodeClientEvent)
			delete(p.Mapping.Directions, EncodeUpstreamEvent)
		}},
		{"credential-field", func(p *ProtocolPackage) { p.Spec.Operations["session"].Session.QueryFields = []string{"api_key"} }},
		{"queue-limit", func(p *ProtocolPackage) { p.Spec.Operations["session"].Session.QueueItems = 0 }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var document ProtocolPackage
			if err := fixtureValue(t, original).Decode(&document); err != nil {
				t.Fatal(err)
			}
			fixture.change(&document)
			if _, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); len(issues) == 0 {
				t.Fatal("invalid session declaration compiled")
			}
		})
	}
	withoutTransport, _ := NewCompiler(DefaultLimits(), nil, nil)
	if _, issues := withoutTransport.CompilePackage(fixtureValue(t, original).Bytes()); len(issues) == 0 {
		t.Fatal("uninstalled transport became available")
	}
	original.Mapping.SessionSamples = nil
	unverified, issues := compiler.CompilePackage(fixtureValue(t, original).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if Verify(t.Context(), unverified).Passed || unverified.SamplesHash() == verified.SamplesHash() {
		t.Fatal("session evidence was skipped or detached from its identity")
	}
}
