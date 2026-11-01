package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

var gatewayPackageNames = []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"}

func seedGatewayPackages(t *testing.T, server *Server) map[string]*protocol.Compiled {
	t.Helper()
	compiled := make(map[string]*protocol.Compiled, len(gatewayPackageNames))
	for _, name := range gatewayPackageNames {
		read := func(part string) protocol.Value {
			t.Helper()
			raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "packages", name, part+".json"))
			if err != nil {
				t.Fatal(err)
			}
			value, err := protocol.ParseValue(raw)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		spec := read("spec")
		var identity struct{ ID string }
		if err := spec.Decode(&identity); err != nil {
			t.Fatal(err)
		}
		raw := mustEncodedProtocolValue(t, map[string]any{"schemaVersion": protocol.ProtocolPackageSchemaVersion, "spec": spec, "mapping": read("mapping")})
		compiled[name] = seedGatewayProtocol(t, server, identity.ID, raw.Bytes(), false)
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
