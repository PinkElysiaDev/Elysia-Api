package server

import (
	"context"
	"strings"
	"testing"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

func TestValidateCustomSourceProtocol(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	if err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID:      "vendor-json",
		Request: relay.CustomProtocolRequest{BodyTemplate: `{}`},
	}); err != nil {
		t.Fatalf("register custom protocol: %v", err)
	}

	item := storage.ModelSource{Platform: "CUSTOM:Vendor-JSON", AutoFetchModels: false}
	if err := validateCustomSourceProtocol(&item); err != nil {
		t.Fatalf("validate registered custom protocol: %v", err)
	}
	if item.Platform != "custom:vendor-json" {
		t.Fatalf("custom platform was not normalized: %q", item.Platform)
	}

	item = storage.ModelSource{Platform: "custom:missing", AutoFetchModels: false}
	if err := validateCustomSourceProtocol(&item); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected unregistered protocol error, got %v", err)
	}

	item = storage.ModelSource{Platform: "custom:vendor-json", AutoFetchModels: true}
	if err := validateCustomSourceProtocol(&item); err == nil || !strings.Contains(err.Error(), "autoFetchModels=false") {
		t.Fatalf("expected manual model requirement, got %v", err)
	}
}

func TestCustomSourceModelDiscoveryRequiresManualModels(t *testing.T) {
	_, err := (&Server{}).fetchModelsFromSource(context.Background(), storage.ModelSource{Platform: "custom:vendor-json"})
	if err == nil || !strings.Contains(err.Error(), "does not define model discovery") {
		t.Fatalf("expected custom discovery error, got %v", err)
	}
}
