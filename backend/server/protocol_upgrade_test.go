package server

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
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
	definition := preview.Definitions["openai-responses"]
	fields, err := definition.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	fields["name"] = protocol.StringValue("Unfinished edited draft")
	fields["id"] = protocol.StringValue("openai-responses-draft")
	value, err := protocol.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	draft, _, err := service.SaveDraft(t.Context(), "openai-responses-draft", value.Bytes(), "")
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
	if active, exists := service.Pin(draft.ProtocolID); exists {
		t.Fatal("migration activated an unrelated editor draft", active.Definition().Name)
	}
	if active, exists := service.Pin("openai-responses"); !exists || active.Definition().Name == "Unfinished edited draft" {
		t.Fatal("preset not activated with shipped content")
	}
}

func TestProtocolUpgradeResetsEditedLegacyPresetToShipped(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	row := storage.CustomProtocol{ID: "openai-responses", Name: "edited", Version: "7", Config: `{"id":"openai-responses","name":"edited","version":"7","request":{"method":"POST","path":"/private","body":{"field":"model"}},"response":{}}`}
	if err := s.store.UpsertCustomProtocol(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil {
		t.Fatal(err)
	}
	// 预置只读：被改动的预置行无条件回归 shipped 版本，迁移保持就绪。
	if !preview.Ready {
		t.Fatal(preview.Issues)
	}
	shipped, err := builtin.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	var expected protocol.Value
	for _, candidate := range shipped {
		var definition protocol.Definition
		if err := candidate.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		if definition.ID == "openai-responses" {
			expected = candidate
			break
		}
	}
	if expected.IsZero() || string(preview.Definitions["openai-responses"].Bytes()) != string(expected.Bytes()) {
		t.Fatal("edited legacy preset was not reset to the shipped definition")
	}
	_, logs, err := s.store.QuerySystemLogs(t.Context(), 20, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	audited := false
	for _, entry := range logs {
		if strings.Contains(entry.Message, "preset protocol reset to shipped version") {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatal("discarded preset edits were not audited")
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
