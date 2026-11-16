package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolRefreshCompilationUsesExactDefinitions(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	document := loadGatewayFixture(t, "text-alpha")
	document.Spec.ID = protocol.PresetAnthropicID
	value := mustEncodedProtocolValue(t, document)
	compilation := service.NewCompilationSession()
	defer compilation.Close()
	plan := &protocolRefreshPlan{store: server.store, context: t.Context(), compilation: compilation,
		definitions: map[string]*protocol.Compiled{}, activations: map[string]protocol.Activation{}}
	if err := plan.ensureProtocol(document.Spec.ID, value, true); err != nil {
		t.Fatal(err)
	}
	selected := plan.definitions[document.Spec.ID]
	reused, issues := compilation.Compile(value)
	if protocol.IssuesError(issues) != nil || reused != selected {
		t.Fatal("identical embedded definition was compiled again", issues)
	}
	spelled := mustProtocolValue(t, " "+value.JSONString())
	first, issues := compilation.Compile(spelled)
	if protocol.IssuesError(issues) != nil || first == selected || first.Hash() != selected.Hash() {
		t.Fatal("different source bytes bypassed compilation or changed normalized identity", issues)
	}
	second, issues := compilation.Compile(spelled)
	if protocol.IssuesError(issues) != nil || second != first {
		t.Fatal("an identical auxiliary source missed the process cache", issues)
	}
	document.Mapping.DSLVersion = 0
	invalid := mustEncodedProtocolValue(t, document)
	if compiled, issues := compilation.Compile(invalid); compiled != nil || protocol.IssuesError(issues) == nil {
		t.Fatal("same identity metadata reused a different invalid definition")
	}
	oversized := mustProtocolValue(t, strings.Repeat(" ", protocol.DefaultLimits().BufferBytes)+value.JSONString())
	if compiled, issues := compilation.Compile(oversized); compiled != nil || protocol.IssuesError(issues) == nil {
		t.Fatal("equivalent definition bypassed its authored byte limit")
	}
	document.Mapping.DSLVersion = protocol.ProtocolDSLVersion
	document.Spec.ID = "custom-refresh"
	custom := mustEncodedProtocolValue(t, document)
	if compiled, issues := compilation.Compile(custom); compiled == nil || protocol.IssuesError(issues) != nil {
		t.Fatal("custom compilation changed the embedded-only memo", issues)
	}
	firstCustom, issues := compilation.Compile(custom)
	secondCustom, otherIssues := compilation.Compile(custom)
	if protocol.IssuesError(issues) != nil || protocol.IssuesError(otherIssues) != nil || firstCustom != secondCustom {
		t.Fatal("identical custom sources missed the process cache")
	}
	fresh := service.NewCompilationSession()
	defer fresh.Close()
	compiled, issues := fresh.Compile(value)
	if protocol.IssuesError(issues) != nil || compiled != selected {
		t.Fatal("independent refresh missed the process cache", issues)
	}
	limits := protocol.DefaultLimits()
	limits.BufferBytes = value.Len() - 1
	limitedCompiler, err := protocol.NewCompiler(limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	limitedService, err := protocol.NewService(limitedCompiler, server.store)
	if err != nil {
		t.Fatal(err)
	}
	limited := limitedService.NewCompilationSession()
	defer limited.Close()
	if compiled, issues := limited.Compile(value); compiled != nil || protocol.IssuesError(issues) == nil {
		t.Fatal("another service inherited a compilation made under larger limits")
	}
}

func TestProtocolRefreshCompilationRereadsRecoveryState(t *testing.T) {
	server, path := newProtocolAdminTestServer(t)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.refreshProtocolRuntimeScope(t.Context(), service, true); err != nil {
		t.Fatal(err)
	}
	db := recoveryDB(t, path)
	compilation := service.NewCompilationSession()
	defer compilation.Close()
	_, definitions, changed, err := server.prepareProtocolRefresh(t.Context(), compilation, true)
	if err != nil || changed {
		t.Fatal("healthy warm prepare changed persistent state", err)
	}
	id := protocol.PresetAnthropicID
	baseline, err := server.store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := server.store.ProtocolRefreshEvidenceBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recoveryExec(t, db, `UPDATE protocol_verification_reports SET report=json_set(report,'$.passed',json('false')) WHERE protocol_id=?`, id)
	plan, _, changed, err := server.prepareProtocolRefresh(t.Context(), compilation, true)
	if err != nil || !changed {
		t.Fatal("cached compilation hid changed verification evidence", err)
	}
	hasRefreshedReport := false
	for _, entry := range plan.Revisions {
		if entry.Revision.ProtocolID == id {
			hasRefreshedReport = entry.Refresh.Report && entry.Report.Passed
		}
	}
	if !hasRefreshedReport {
		t.Fatal("changed evidence did not trigger the existing verifier")
	}
	plan.Baseline, plan.EvidenceBaseline = baseline, evidence
	if err := server.store.RefreshProtocolRuntime(t.Context(), plan); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("compilation reuse bypassed evidence CAS", err)
	}
	recoveryExec(t, db, `UPDATE protocol_revisions SET definition=json_set(definition,'$.spec.id','wrong-id') WHERE protocol_id=? AND content_hash=?`, id, definitions[id].Hash())
	plan, _, changed, err = server.prepareProtocolRefresh(t.Context(), compilation, true)
	if err != nil || !changed {
		t.Fatal("cached compilation hid changed stored definition bytes", err)
	}
	hasRepairedDefinition := false
	for _, entry := range plan.Revisions {
		if entry.Revision.ProtocolID == id {
			hasRepairedDefinition = entry.Refresh.RepairPreset && entry.Revision.Hash == definitions[id].Hash()
		}
	}
	if !hasRepairedDefinition {
		t.Fatal("changed preset identity did not require repair")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := server.prepareProtocolRefresh(ctx, compilation, true); !errors.Is(err, context.Canceled) {
		t.Fatal("warm compilation bypassed canceled repository reads", err)
	}
	stored, err := server.store.ReadProtocolRevision(t.Context(), id, definitions[id].Hash())
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Spec struct {
			ID string `json:"id"`
		} `json:"spec"`
	}
	if err := stored.Definition.Decode(&identity); err != nil || identity.Spec.ID != "wrong-id" {
		t.Fatal("prepare or failed commit changed stored corruption", err)
	}
}

func TestProtocolRefreshCompilationPublishesPreparedPointers(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.refreshProtocolRuntimeScope(t.Context(), service, true); err != nil {
		t.Fatal(err)
	}
	if err := service.ReloadAvailable(t.Context(), requiredPresetIDs...); err != nil {
		t.Fatal(err)
	}
	previous := service.View()
	compilation := service.NewCompilationSession()
	defer compilation.Close()
	_, definitions, hasChanges, err := server.prepareProtocolRefresh(t.Context(), compilation, true)
	if err != nil || hasChanges {
		t.Fatal("healthy preparation changed persistent state", err)
	}
	if err := compilation.ReloadAvailable(t.Context(), requiredPresetIDs...); err != nil {
		t.Fatal(err)
	}
	for _, id := range requiredPresetIDs {
		published, hasPublished := service.Pin(id)
		prior, hasPrior := previous.Pin(id)
		if !hasPublished || !hasPrior || published != definitions[id] || published != prior {
			t.Fatalf("preset %s was not handed from real preparation to publication", id)
		}
	}
}

func TestProtocolRefreshAndPublishPreservesSnapshotOnPreparationError(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.refreshAndPublishProtocols(t.Context(), service, true); err != nil {
		t.Fatal(err)
	}
	previous := service.View()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := server.refreshAndPublishProtocols(ctx, service, true); !errors.Is(err, context.Canceled) {
		t.Fatal("combined helper lost the preparation error", err)
	}
	current := service.View()
	if len(current.IDs()) != len(previous.IDs()) {
		t.Fatal("preparation error changed the published protocol set")
	}
	for _, id := range previous.IDs() {
		prior, _ := previous.Pin(id)
		published, hasPublished := current.Pin(id)
		if !hasPublished || published != prior {
			t.Fatalf("preparation error replaced preset %s", id)
		}
	}
}
