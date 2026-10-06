package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func presetDefinition(t *testing.T, id string) protocol.Definition {
	t.Helper()
	definitions, err := builtin.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range definitions {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		if definition.ID == id {
			return definition
		}
	}
	t.Fatalf("missing preset %s", id)
	return protocol.Definition{}
}

func compileFixtureDefinition(t *testing.T, definition protocol.Definition) *protocol.Compiled {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), builtin.Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustEncodedProtocolValue(t, definition)
	compiled, issues := compiler.Compile(raw.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}
