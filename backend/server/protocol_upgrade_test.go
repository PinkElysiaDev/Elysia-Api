package server

import (
	"errors"
	"os"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestProtocolUpgradePreviewPreservesDraftAndLegacyEdits(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.seedPresetProtocols()
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Ready {
		t.Fatal(preview.Issues)
	}
	if receipt, err := s.store.ProtocolUpgradeStatus(t.Context()); err != nil || receipt != nil {
		t.Fatal("preview mutated migration status", receipt, err)
	}
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	definition := preview.Definitions["responses-api"]
	fields, err := definition.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	fields["name"] = protocol.StringValue("Unfinished edited draft")
	value, err := protocol.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	draft, _, err := service.SaveDraft(t.Context(), "responses-api", value.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{Baseline: preview.Baseline}); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("stale preview accepted", err)
	}
	preview, err = s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil || !preview.Ready {
		t.Fatal(preview, err)
	}
	receipt, err := s.store.ApplyProtocolUpgrade(t.Context(), preview.plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receipt.Backup); err != nil {
		t.Fatal(err)
	}
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := service.ReadDraft(t.Context(), draft.ProtocolID)
	if err != nil || saved.Hash != draft.Hash {
		t.Fatal("migration overwrote editor draft", saved, err)
	}
	active, exists := service.Pin(draft.ProtocolID)
	if !exists || active.Definition().Name == "Unfinished edited draft" {
		t.Fatal("migration activated unfinished editor state")
	}
}

func TestProtocolUpgradeEditedLegacyRequiresExplicitReplacement(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	row := storage.CustomProtocol{ID: "responses-api", Name: "edited", Version: "7", Config: `{"id":"responses-api","name":"edited","version":"7","request":{"method":"POST","path":"/private","body":{"field":"model"}},"response":{}}`}
	if err := s.store.UpsertCustomProtocol(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ready || len(preview.Issues) == 0 {
		t.Fatal("edited legacy replaced silently")
	}
	if receipt, err := s.store.ProtocolUpgradeStatus(t.Context()); err != nil || receipt != nil {
		t.Fatal(receipt, err)
	}
	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Config != row.Config {
		t.Fatal("preview changed legacy content", rows, err)
	}
}

func TestProtocolUpgradeValidatesWholeBindingGraph(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	if err := s.store.UpsertSource(t.Context(), storage.ModelSource{ID: "disabled", Name: "disabled", Platform: "openai", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Bindings) != 1 || preview.Bindings[0].SourceID != "disabled" {
		t.Fatal("disabled source omitted", preview.Bindings)
	}
	if !preview.Ready {
		for _, binding := range preview.Bindings {
			for _, report := range binding.Combinations {
				t.Log(report.TargetHash, report.Issues)
			}
		}
		t.Fatal(preview.Issues)
	}
}
