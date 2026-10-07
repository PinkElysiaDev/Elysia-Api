package server

import (
	"errors"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// 预置只读的持久化边界：探测证据只允许落在当前激活（shipped）修订上，
// 旧政策遗留的预置 draft 不能借 test 探测把已废弃的编辑写回修订历史。
func TestProbeProtocolPersistsPresetEvidenceOnlyForActiveRevision(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()
	definition := presetDefinition(t, "chat-completions-api")
	shipped := mustEncodedProtocolValue(t, definition)
	compiled := compileFixtureDefinition(t, definition)
	if err := server.store.SaveProtocolRevision(ctx, protocol.Revision{ProtocolID: definition.ID, Hash: compiled.Hash(), Definition: shipped, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	report := protocol.VerificationReport{DefinitionHash: compiled.Hash(), CompilerVersion: protocol.CompilerVersion, SamplesHash: compiled.SamplesHash(), Kind: protocol.OfflineVerification, Passed: true, VerifiedAt: time.Now().UTC()}
	if err := server.store.SaveProtocolReport(ctx, definition.ID, compiled.Hash(), report); err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.SetProtocolActivation(ctx, definition.ID, compiled.Hash(), ""); err != nil {
		t.Fatal(err)
	}
	// A dead endpoint still exercises persistence: the probe records the failed
	// upstream attempt instead of aborting before the evidence block.
	probe := func(value protocol.Value) {
		t.Helper()
		if _, err := server.probeProtocol(ctx, protocolProbeInput{Definition: value, Operation: "models", BaseURL: "http://127.0.0.1:1"}); err != nil {
			t.Fatalf("probeProtocol: %v", err)
		}
	}
	edited := definition
	edited.Name = "abandoned preset draft"
	editedValue := mustEncodedProtocolValue(t, edited)
	editedCompiled := compileFixtureDefinition(t, edited)
	if editedCompiled.Hash() == compiled.Hash() {
		t.Fatal("precondition: name edit must change the definition hash")
	}
	probe(editedValue)
	if _, err := server.store.ReadProtocolRevision(ctx, definition.ID, editedCompiled.Hash()); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatal("legacy preset draft revision persisted", err)
	}
	if reports, err := server.store.ListProtocolUpstreamReports(ctx, definition.ID, editedCompiled.Hash()); err != nil || len(reports) != 0 {
		t.Fatal("legacy preset draft report persisted", err, reports)
	}
	probe(shipped)
	if reports, err := server.store.ListProtocolUpstreamReports(ctx, definition.ID, compiled.Hash()); err != nil || len(reports) != 1 {
		t.Fatal("active preset probe evidence missing", err, reports)
	}
}
