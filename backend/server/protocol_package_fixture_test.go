package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

var gatewayPackageNames = []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"}

func loadGatewayPackage(t *testing.T, directory string) protocol.ProtocolPackage {
	t.Helper()
	document := protocol.ProtocolPackage{SchemaVersion: protocol.ProtocolPackageSchemaVersion}
	for _, source := range []struct {
		name   string
		target any
	}{{"spec", &document.Spec}, {"mapping", &document.Mapping}} {
		raw, err := os.ReadFile(filepath.Join("..", "protocol", directory, source.name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := sonic.ConfigStd.Unmarshal(raw, source.target); err != nil {
			t.Fatal(err)
		}
	}
	return document
}

func activateGatewayPackage(t *testing.T, server *Server, document protocol.ProtocolPackage) *protocol.Compiled {
	t.Helper()
	raw := fixturePackageValue(t, document)
	compiled := seedGatewayProtocol(t, server, document.Spec.ID, raw.Bytes(), true)
	if compiled.CoreVersion() != protocol.ProtocolCoreVersion {
		t.Fatal("package fixture selected a legacy core")
	}
	return compiled
}

func defaultGatewayPackages(t *testing.T, server *Server) map[string]*protocol.Compiled {
	t.Helper()
	if err := server.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	compiled := make(map[string]*protocol.Compiled, len(gatewayPackageNames))
	for _, name := range gatewayPackageNames {
		document := loadGatewayPackage(t, filepath.Join("builtin", "packages", name))
		var exists bool
		compiled[name], exists = service.Pin(document.Spec.ID)
		if !exists || compiled[name].CoreVersion() != protocol.ProtocolCoreVersion {
			t.Fatal("default startup did not activate the core2 package", name)
		}
		for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest, protocol.DecodeResponse, protocol.EncodeResponse, protocol.DecodeEvent, protocol.EncodeEvent} {
			if !compiled[name].Supports(direction) {
				t.Fatal("default package is missing a wire/core direction", name, direction)
			}
		}
	}
	return compiled
}
