package server

import (
	"sync"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayConversionPlanGenerationAndIsolation(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	source := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	target := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-beta"))
	model := config.ModelRef{SourceID: "source", ID: "model", Name: "model"}
	before, err := s.pinGatewayConversions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first, err := before.resolve(source, target, model, "generate", protocol.HTTPJSON)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			snapshot, err := s.pinGatewayConversions(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if snapshot != before {
				t.Error("unchanged generation rebuilt snapshot")
			}
			local, err := snapshot.resolve(source, target, model, "generate", protocol.HTTPJSON)
			if err != nil {
				t.Error(err)
				return
			}
			if local == first || local.Hash != first.Hash {
				t.Error("execution instance or policy identity differs")
			}
			local.VerifiedProviderRules = map[string]bool{"private": true}
			local.VerificationContext = &protocol.ConversionContext{Model: "private"}
		})
	}
	wg.Wait()
	if len(before.plans) != 1 || first.VerifiedProviderRules != nil || first.VerificationContext != nil {
		t.Fatal("shared request state")
	}
	binding := storage.ProtocolBinding{Kind: "source", SourceID: model.SourceID, Conversion: &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "override", Mode: "strict"}}}
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	after, err := s.pinGatewayConversions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, err := after.resolve(source, target, model, "generate", protocol.HTTPJSON)
	if err != nil {
		t.Fatal(err)
	}
	if after == before || after.generation <= before.generation || current.Hash == first.Hash || current.Policy.Mode != "strict" {
		t.Fatal("binding update did not invalidate compiled policy")
	}
	pinned, err := before.resolve(source, target, model, "generate", protocol.HTTPJSON)
	if err != nil || pinned.Hash != first.Hash {
		t.Fatal("in-flight generation changed", err)
	}
	definition := loadGatewayDefinition(t, "text-beta")
	definition.Name += " updated"
	revised := activateGatewayDefinition(t, s, definition)
	if _, err := after.resolve(source, revised, model, "generate", protocol.HTTPJSON); err != nil {
		t.Fatal(err)
	}
	model.Name = "changed-model"
	if _, err := after.resolve(source, revised, model, "stream", protocol.SSE); err != nil {
		t.Fatal(err)
	}
	if len(after.plans) != 3 {
		t.Fatal("revision/model/operation/transport reused stale plan")
	}
	// Readers must see bindings and their derived program from one generation,
	// including when a writer commits while another reader is compiling.
	wg.Go(func() {
		for i := range 12 {
			next := binding
			mode := "compatible"
			if i%2 == 0 {
				mode = "strict"
			}
			next.Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "override", Mode: mode}}
			if err := s.store.SaveProtocolBinding(t.Context(), next); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 12 {
				snapshot, err := s.pinGatewayConversions(t.Context())
				if err != nil {
					t.Error(err)
					return
				}
				local, err := snapshot.resolve(source, revised, model, "generate", protocol.HTTPJSON)
				if err != nil {
					t.Error(err)
					return
				}
				for _, pinned := range snapshot.bindings {
					if pinned.SourceID == model.SourceID && pinned.Conversion != nil && local.Policy.Mode != pinned.Conversion.Overrides.Mode {
						t.Error("mixed generations during concurrent update")
					}
				}
			}
		})
	}
	wg.Wait()
}
