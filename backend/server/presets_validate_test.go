package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestPresetProtocolsValidate(t *testing.T) {
	definitions, err := builtin.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 4 {
		t.Fatalf("presets = %d, want 4", len(definitions))
	}
	for _, value := range definitions {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		t.Run(definition.ID, func(t *testing.T) {
			compiled := compileFixtureDefinition(t, definition)
			if report := protocol.Verify(t.Context(), compiled); !report.Passed {
				t.Fatalf("preset invalid: %+v", report.Issues)
			}
		})
	}
}
