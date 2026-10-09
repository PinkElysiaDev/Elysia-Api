package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestRuntimeRefreshRepairsResponsesPhaseLifecycleEvidence(t *testing.T) {
	for _, version := range []string{"dev28", "dev29"} {
		t.Run(version, func(t *testing.T) { refreshResponsesMessageLifecycleEvidence(t, version) })
	}
}

func refreshResponsesMessageLifecycleEvidence(t *testing.T, version string) {
	s, _ := newProtocolAdminTestServer(t)
	d := presetDefinition(t, protocol.PresetResponsesID)
	d.ID = "existing-custom-responses"
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "testdata", "responses-"+version+"-message-samples.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oldSamples []protocol.Sample
	if err = json.Unmarshal(raw, &oldSamples); err != nil {
		t.Fatal(err)
	}
	for i, sample := range d.Samples {
		for _, old := range oldSamples {
			if old.ID == sample.ID {
				d.Samples[i] = old
			}
		}
	}
	old := persistPreviousRevision(t, s, mustEncodedProtocolValue(t, d))
	draft, err := s.store.ReadProtocolDraft(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, _ := s.protocolService()
	current, exists := service.Pin(d.ID)
	if !exists || current.Hash() == old.Hash() {
		t.Fatal("old custom protocol not advanced to corrected lifecycle evidence")
	}
	if _, err = s.store.ReadProtocolRevision(t.Context(), d.ID, old.Hash()); err != nil {
		t.Fatal("original revision lost", err)
	}
	keptDraft, err := s.store.ReadProtocolDraft(t.Context(), d.ID)
	if err != nil || !reflect.DeepEqual(draft, keptDraft) {
		t.Fatal("operator draft changed", err)
	}
	report, err := s.store.ReadProtocolReport(t.Context(), d.ID, current.Hash())
	if err != nil || !report.Passed || report.CompilerVersion != protocol.CompilerVersion {
		t.Fatal("missing current evidence", err)
	}
	baseline, err := s.store.ProtocolRefreshEvidenceBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err = s.reloadProtocolRuntime(t.Context()); err != nil {
			t.Fatal(err)
		}
		after, err := s.store.ProtocolRefreshEvidenceBaseline(t.Context())
		if err != nil || after != baseline {
			t.Fatal("repeated evidence upgrade", i, err)
		}
		afterState, err := s.store.ProtocolUpgradeBaseline(t.Context())
		if err != nil || afterState != state {
			t.Fatal("repeated state update", i, err)
		}
	}
}
