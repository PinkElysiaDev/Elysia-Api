package builtin

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageSourceStreamMigration(t *testing.T) {
	candidate := compileBuiltinPackage(t, "anthropic")
	document, _ := candidate.Package()
	for _, sample := range document.Mapping.Samples {
		if sample.ID != "declared-stream-text-decode" && sample.ID != "declared-stream-tool-decode" && sample.ID != "declared-signed-stream-reasoning-decode" && sample.ID != "declared-stream-encrypted-decode" {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var input []p.Value
			if err := sample.Input.Decode(&input); err != nil {
				t.Fatal(err)
			}
			raws := make([]string, len(input))
			for index, frame := range input {
				raws[index] = frame.JSONString()
			}
			frames, response := decodePackageStream(t, candidate, raws, sample.Scope)
			leaves := messageLeaves(t, response)
			if len(leaves) != 1 || !response.ID.IsString("r") || !response.Model.IsString("m") || !response.Status.IsString("completed") {
				t.Fatal("source message lost its envelope or block", response)
			}
			first := frames[0].Events
			last := frames[len(frames)-1].Events
			if len(first) != 2 || first[0].Type != p.ResponseStarted || first[0].Usage == nil || first[0].Usage.Input == nil || first[0].Usage.Input.Count != 0 || first[1].Type != p.ItemStarted || !first[1].ItemID.IsString("message") || len(last) != 2 || last[0].Type != p.ItemFinished || !last[0].ItemID.IsString("message") || last[1].Type != p.ResponseFinished {
				t.Fatal("message lifecycle or initial usage ownership changed", first, last)
			}
			switch sample.ID {
			case "declared-stream-text-decode":
				if leaves[0].Kind != p.TextNode || !leaves[0].Payload.IsString("hi") || response.Usage.Input.Count != 20 || response.Usage.Output.Count != 2 {
					t.Fatal("text or token usage was lost", leaves[0], response.Usage)
				}
			case "declared-stream-tool-decode":
				call := leaves[0]
				if call.Kind != p.ToolCallNode || !call.CallID.IsString("call") || !call.Name.IsString("lookup") || call.Input == nil || call.Input.Value != testValue(t, `{"n":9007199254740993}`) {
					t.Fatal("tool association or exact arguments changed", call)
				}
			case "declared-signed-stream-reasoning-decode":
				thought := leaves[0]
				if thought.Kind != p.ReasoningNode || !thought.Payload.IsString("hi") || len(thought.Resources) != 1 || thought.Resources[0].Kind != "signature" || !thought.Resources[0].ID.IsString("signed") {
					t.Fatal("thinking or its signature changed", thought)
				}
			case "declared-stream-encrypted-decode":
				thought := leaves[0]
				if thought.Kind != p.ReasoningNode || len(thought.Resources) != 1 || thought.Resources[0].Kind != "encrypted_content" || !thought.Resources[0].ID.IsString("sealed") || thought.Resources[0].Scope != sample.Scope {
					t.Fatal("encrypted thought left its original scope", thought)
				}
			}
			if native := encodePackageStream(t, candidate, frames, sample.Scope, true); !slices.Equal(native, raws) {
				t.Fatal("explicit source containers changed native frame bytes", native)
			}
		})
	}
}

func TestAnthropicPackageSparseStreamIdentities(t *testing.T) {
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []struct{ name, message, index string }{
		{"missing-index", `"id":"r","model":"m"`, ""},
		{"null-index", `"id":"r","model":"m"`, `,"index":null`},
		{"missing-identity", "", `,"index":0`},
		{"null-identity", `"id":null,"model":null`, `,"index":0`},
		{"empty-identity", `"id":"","model":""`, `,"index":0`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			defer options.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(options.Scope)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := testValue(t, "{"+fixture.message+"}").ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			collector, err := p.NewResponseCollector(candidate.Target(p.EncodeEvent, options.Scope), candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			for index, raw := range []string{
				fmt.Sprintf(`{"type":"message_start","message":{%s}}`, fixture.message),
				fmt.Sprintf(`{"type":"content_block_start"%s,"content_block":{"type":"text","text":""}}`, fixture.index),
				fmt.Sprintf(`{"type":"content_block_delta"%s,"delta":{"type":"text_delta","text":"hello"}}`, fixture.index),
				fmt.Sprintf(`{"type":"content_block_stop"%s}`, fixture.index),
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
				`{"type":"message_stop"}`,
			} {
				frame, err := wire.DecodeFrame(t.Context(), testValue(t, raw), options)
				if index == 1 && (fixture.name == "missing-index" || fixture.name == "null-index") {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.UpstreamContractViolation || failure.Issues[0].Path != "/index" {
						t.Fatal("declared contract accepted an absent block index", err)
					}
					return
				}
				if err != nil {
					t.Fatal("declared mapping", err)
				}
				for _, event := range frame.Events {
					if event.ResponseID != identity["id"] || event.Response != nil && event.Response.Model != identity["model"] {
						t.Fatal("sparse external identity was replaced by a synthetic value", event)
					}
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			response, err := collector.Finish()
			if err != nil {
				t.Fatal(err)
			}
			leaves := messageLeaves(t, response)
			if len(leaves) != 1 || !leaves[0].Payload.IsString("hello") {
				t.Fatal("sparse identity lost the generated content", response)
			}
		})
	}
}

func TestAnthropicPackageSignatureSource(t *testing.T) {
	candidate := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "provider", Account: "account", Model: "m"}
	raws := anthropicSignatureFrames()
	raws[0] = `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}`
	raws[len(raws)-2] = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`
	frames, response := decodePackageStream(t, candidate, raws, scope)
	leaves := messageLeaves(t, response)
	if len(leaves) != 1 || !leaves[0].Payload.IsString("reason") || len(leaves[0].Resources) != 1 || !leaves[0].Resources[0].ID.IsString("first-second") || leaves[0].Resources[0].Scope != scope {
		t.Fatal("signature fragments lost their original scope or content", response)
	}
	t.Run("semantic", func(t *testing.T) {
		rendered := encodePackageStream(t, candidate, frames, scope, false)
		_, rebuilt := decodePackageStream(t, candidate, rendered, scope)
		leaves := messageLeaves(t, rebuilt)
		if len(leaves) != 1 || len(leaves[0].Resources) != 1 || !leaves[0].Resources[0].ID.IsString("first-second") || rebuilt.Usage == nil || rebuilt.Usage.Output == nil || rebuilt.Usage.Output.Count != 2 {
			t.Fatal("semantic rendering changed the completed signature or observed usage", rebuilt)
		}
	})
	t.Run("native", func(t *testing.T) {
		if native := encodePackageStream(t, candidate, frames, scope, true); !slices.Equal(native, raws) {
			t.Fatal("native rendering changed signature delta bytes", native)
		}
	})
}

// Version is the reviewed core contract difference; payload and provenance
// remain in the comparison. Nested responses are copied before adjustment.
func streamEventsAtVersion(events []p.Event, version int) []p.Event {
	output := slices.Clone(events)
	for index := range output {
		output[index].SchemaVersion = version
		if output[index].Response != nil {
			response := *output[index].Response
			response.SchemaVersion = version
			output[index].Response = &response
		}
	}
	return output
}

func encodeMigratedStream(t *testing.T, name string, events []p.Event, scope p.Scope) p.Value {
	t.Helper()
	target := testCompiled(t, name)
	options := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
	defer options.State.Release()
	var frames []p.Value
	for _, event := range events {
		encoded, err := target.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, encoded...)
	}
	tail, err := target.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.EncodeValue(append(frames, tail...))
	if err != nil {
		t.Fatal(err)
	}
	return output
}
