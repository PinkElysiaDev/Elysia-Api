package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// These original fixtures remain in the legacy differential suite. Their
// complete replacements supply activation evidence without changing that suite.
var anthropicComponentResponseSamples = map[string]struct{ replacement, finalPath string }{
	"tool-encode-response":                        {"completed-tool-encode-response", "/usage"},
	"reasoning-response-encode":                   {"completed-reasoning-response-encode", "/content/0/signature"},
	"signature-reasoning-response-encode":         {"completed-signature-reasoning-response-encode", "/usage"},
	"encrypted_content-reasoning-response-encode": {"completed-encrypted-reasoning-response-encode", "/usage"},
}

// The fixtures in the package must also pass the actual verification pipeline,
// including roundtrip and declared-capability checks used during activation.
func TestBuiltinPackageJSONEvidence(t *testing.T) {
	for _, name := range []string{"anthropic", "openai-chat", "openai-responses", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			document, _ := compiled.Package()
			if len(document.Mapping.Samples) == 0 {
				t.Fatal("package has no authored generation evidence")
			}
			samples := make(map[string]bool, len(document.Mapping.Samples))
			for _, sample := range document.Mapping.Samples {
				if sample.Direction == p.DecodeRequest || sample.Direction == p.EncodeRequest || sample.Direction == p.DecodeResponse || sample.Direction == p.EncodeResponse {
					samples[sample.ID] = false
				}
			}
			if name == "anthropic" {
				for original, fixture := range anthropicComponentResponseSamples {
					if _, exists := samples[original]; exists {
						t.Errorf("component-only fixture %s was promoted to final delivery evidence", original)
					}
					if _, exists := samples[fixture.replacement]; !exists {
						t.Errorf("complete delivery replacement %s is missing", fixture.replacement)
					}
				}
			}
			report := p.Verify(t.Context(), compiled)
			for _, check := range report.Checks {
				if _, exists := samples[check.SampleID]; exists {
					samples[check.SampleID] = check.Passed
				}
			}
			for id, isPassed := range samples {
				if !isPassed {
					t.Errorf("authored JSON fixture %s failed verification", id)
				}
			}
			if t.Failed() {
				for _, issue := range report.Issues {
					t.Log(issue)
				}
			}
		})
	}
}
