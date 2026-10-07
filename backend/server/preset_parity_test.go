package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// A definition ID identifies a revision, not its wire semantics. Copies must
// pass the same capability evidence and compose with the shipped definition.
func TestPresetCopiesPreserveVerifiedCapabilities(t *testing.T) {
	for _, id := range []string{"openai-chat-completions", "openai-responses", "anthropic-messages", "google-generate-content"} {
		t.Run(id, func(t *testing.T) {
			definition := presetDefinition(t, id)
			original := compileFixtureDefinition(t, definition)
			definition.ID = "User-" + id
			definition.Version = "user-revision"
			copied := compileFixtureDefinition(t, definition)
			if report := protocol.Verify(t.Context(), copied); !report.Passed {
				t.Fatalf("copied definition verification: %+v", report.Issues)
			}
			for _, pair := range [][2]*protocol.Compiled{{original, copied}, {copied, original}} {
				report := protocol.VerifyCombination(t.Context(), pair[0], pair[1])
				if !report.Passed {
					t.Fatalf("%s -> %s: %+v", pair[0].Identity().DefinitionID, pair[1].Identity().DefinitionID, report.Issues)
				}
			}
		})
	}
}
