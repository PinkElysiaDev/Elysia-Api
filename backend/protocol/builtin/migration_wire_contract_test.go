package builtin

import (
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func checkMigrationWireCorpus(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/migration-wire-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Protocols map[string][]struct {
			ID          string
			Direction   p.Direction
			Wire        p.Value
			RebuiltWire p.Value
		}
	}
	if err := testValue(t, string(raw)).Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	compiled := testCompiled(t, name)
	samples := map[string]p.Sample{}
	for _, sample := range packageMapping(compiled).Samples {
		samples[sample.ID] = sample
	}
	if len(corpus.Protocols[name]) == 0 {
		t.Fatal("missing original migration corpus", name)
	}
	for _, fixture := range corpus.Protocols[name] {
		t.Run(fixture.ID, func(t *testing.T) {
			if component, exists := anthropicComponentResponseSamples[fixture.ID]; name == Anthropic && exists {
				requireContextIssue(t, compiled.ValidateWireOutput(fixture.Direction, fixture.Wire), p.ConversionRejected, component.finalPath)
				return
			}
			sample, exists := samples[fixture.ID]
			if !exists || sample.Direction != fixture.Direction {
				t.Fatal("original migration sample disappeared or changed direction", fixture.ID)
			}
			wire := sample.Input
			if sample.Direction == p.EncodeRequest || sample.Direction == p.EncodeResponse {
				wire = sample.Expected
				expected := fixture.RebuiltWire
				if expected.IsZero() {
					expected = fixture.Wire
				}
				checkMigrationWithoutNative(t, compiled, sample, expected)
			}
			sameJSON(t, wire.Bytes(), fixture.Wire.JSONString())
		})
	}
	if report := p.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal("declared semantic expectations did not verify", report.Issues)
	}
}

func checkMigrationWithoutNative(t *testing.T, compiled *p.Compiled, sample p.Sample, expected p.Value) {
	t.Helper()
	options := fixtureOptions(p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
	var wire []byte
	var err error
	switch sample.Direction {
	case p.EncodeRequest:
		var request p.Request
		if err := sample.Input.Decode(&request); err != nil {
			t.Fatal(err)
		}
		request.Native = nil
		wire, err = compiled.EncodeRequest(t.Context(), &request, options)
	case p.EncodeResponse:
		var response p.Response
		if err := sample.Input.Decode(&response); err != nil {
			t.Fatal(err)
		}
		response.Native = nil
		wire, err = compiled.EncodeResponse(t.Context(), &response, options)
	}
	if err != nil {
		t.Fatal("native-free migration boundary", err)
	}
	sameJSON(t, wire, expected.JSONString())
}

func TestChatPackageDraftMigration(t *testing.T) {
	checkMigrationWireCorpus(t, Chat)
	checkMigrationBoundaries(t, Chat)
}

func TestResponsesPackageDraftMigration(t *testing.T) {
	checkMigrationWireCorpus(t, Responses)
	checkMigrationBoundaries(t, Responses)
}

func TestAnthropicPackageDraftMigration(t *testing.T) {
	checkMigrationWireCorpus(t, Anthropic)
	checkMigrationBoundaries(t, Anthropic)
}

func TestGeminiPackageDraftMigration(t *testing.T) {
	checkMigrationWireCorpus(t, Gemini)
	checkMigrationBoundaries(t, Gemini)
}

func checkMigrationBoundaries(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/migration-boundaries.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Protocols map[string][]struct {
			ID          string
			Wire        p.Value
			RawWire     string
			RebuiltWire p.Value
			IsResponse  bool `json:"response"`
			RejectPath  string
		}
	}
	if err := testValue(t, string(raw)).Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	compiled := testCompiled(t, name)
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}})
	for _, fixture := range corpus.Protocols[name] {
		t.Run("boundary/"+fixture.ID, func(t *testing.T) {
			var output []byte
			var err error
			if fixture.IsResponse {
				var response *p.Response
				response, err = compiled.DecodeResponse(t.Context(), []byte(fixture.RawWire), options)
				if err == nil {
					response.Native = nil
					output, err = compiled.EncodeResponse(t.Context(), response, options)
				}
			} else {
				var request *p.Request
				request, err = compiled.DecodeRequest(t.Context(), []byte(fixture.RawWire), options)
				if err == nil {
					request.Native = nil
					output, err = compiled.EncodeRequest(t.Context(), request, options)
				}
			}
			if fixture.RejectPath != "" {
				if err == nil || !strings.Contains(err.Error(), fixture.RejectPath) {
					t.Fatal("original boundary was not refused at its field", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := fixture.RebuiltWire
			if expected.IsZero() {
				expected = fixture.Wire
			}
			sameJSON(t, output, expected.JSONString())
		})
	}
}
