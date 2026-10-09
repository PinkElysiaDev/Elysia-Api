package server

import (
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestRuntimeRefreshCorrectsFixtureWithBindingAndDraftPreserved(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	d := presetDefinition(t, protocol.PresetResponsesID)
	d.ID = "operator-responses-fixture"
	changed := false
	for i, sample := range d.Samples {
		if sample.ID != "stream-text-encode" {
			continue
		}
		var frames []protocol.Value
		if err := sample.Expected.Decode(&frames); err != nil {
			t.Fatal(err)
		}
		for j, frame := range frames {
			f, _ := frame.ReadObject()
			if f["type"] != protocol.StringValue("response.content_part.added") {
				continue
			}
			part, _ := f["part"].ReadObject()
			delete(part, "annotations")
			f["part"], _ = protocol.EncodeValue(part)
			frames[j], _ = protocol.EncodeValue(f)
			changed = true
		}
		d.Samples[i].Expected, _ = protocol.EncodeValue(frames)
	}
	if !changed {
		t.Fatal("missing historical fixture shape")
	}
	old := persistPreviousRevision(t, s, mustEncodedProtocolValue(t, d))
	draft, err := s.store.ReadProtocolDraft(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := storage.ProtocolBinding{Kind: "source", SourceID: "fixture-source", Binding: protocol.Binding{ProtocolID: d.ID, RevisionHash: old.Hash(), Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true, protocol.FunctionToolsCapability: false}, Transports: []protocol.Transport{protocol.HTTPJSON}}}
	if err = s.store.SaveProtocolBinding(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	unbound := storage.ProtocolBinding{Kind: "model", SourceID: "fixture-source", ModelID: "leave-unbound", Unbound: true}
	if err = s.store.SaveProtocolBinding(t.Context(), unbound); err != nil {
		t.Fatal(err)
	}
	if err = s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, _ := s.protocolService()
	current, ok := service.Pin(d.ID)
	if !ok || current.Hash() == old.Hash() {
		t.Fatal("corrected revision not activated")
	}
	stored, err := s.store.ReadProtocolRevision(t.Context(), d.ID, old.Hash())
	if err != nil || stored.Hash != old.Hash() {
		t.Fatal("historical revision lost", err)
	}
	after, err := s.store.ReadProtocolDraft(t.Context(), d.ID)
	if err != nil || !reflect.DeepEqual(after, draft) {
		t.Fatal("draft overwritten", err)
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 2 {
		t.Fatal(err, bindings)
	}
	for _, entry := range bindings {
		if entry.Unbound {
			if !reflect.DeepEqual(entry, unbound) {
				t.Fatal("unbound changed")
			}
			continue
		}
		if entry.Binding.RevisionHash != current.Hash() {
			t.Fatal("binding still uses invalid fixture revision")
		}
		entry.Binding.RevisionHash = b.Binding.RevisionHash
		if !reflect.DeepEqual(entry.Binding, b.Binding) {
			t.Fatal("manual capabilities changed")
		}
		if !hasPassingGatewayCombination(entry.Combinations) {
			t.Fatal("binding lacks verified text route", entry.Combinations)
		}
	}
	if err = s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	stable, _ := service.Pin(d.ID)
	if stable.Hash() != current.Hash() {
		t.Fatal("repair repeated on reload")
	}
}
