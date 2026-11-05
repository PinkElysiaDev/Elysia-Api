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
	raw := mustEncodedProtocolValue(t, document)
	compiled := seedGatewayProtocol(t, server, document.Spec.ID, raw.Bytes(), true)
	if compiled.CoreVersion() != protocol.ProtocolCoreVersion {
		t.Fatal("package fixture selected a legacy core")
	}
	return compiled
}

func seedGatewayPackages(t *testing.T, server *Server) map[string]*protocol.Compiled {
	t.Helper()
	compiled := make(map[string]*protocol.Compiled, len(gatewayPackageNames))
	for _, name := range gatewayPackageNames {
		document := loadGatewayPackage(t, filepath.Join("builtin", "packages", name))
		raw := mustEncodedProtocolValue(t, document)
		compiled[name] = seedGatewayProtocol(t, server, document.Spec.ID, raw.Bytes(), false)
		for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest, protocol.DecodeResponse, protocol.EncodeResponse, protocol.DecodeEvent, protocol.EncodeEvent} {
			if compiled[name].Codec(direction) != "" {
				t.Fatal("package used a legacy codec", name, direction)
			}
		}
	}
	service, _ := server.protocolService()
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	return compiled
}
