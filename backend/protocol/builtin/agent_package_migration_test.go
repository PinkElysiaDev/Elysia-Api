package builtin

import (
	"bytes"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageAgentAndModelDiscoveryMigration(t *testing.T) {
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range definitions {
		var definition p.Definition
		if err := raw.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		name := definition.ID
		if name == "openai-chat-completions" {
			name = "openai-chat"
		} else if name == "anthropic-messages" {
			name = "anthropic"
		}
		t.Run(name, func(t *testing.T) {
			legacy, issues := compiler.Compile(raw.Bytes())
			if err := p.IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			draft := compileBuiltinPackage(t, name)
			projection, err := p.ResolveConversion(p.DefaultConversionPolicy(legacy, legacy))
			if err != nil {
				t.Fatal(err)
			}
			document, _ := draft.Package()
			if !draft.HasAgentPolicy() || !equalAgentMigrationValues(t, document.Mapping.Agent.Samples, definition.Agent.Samples) || !equalAgentMigrationValues(t, document.Mapping.ModelSamples, definition.ModelSamples) {
				t.Fatal("declared Agent or discovery evidence changed")
			}
			for _, sample := range definition.ModelSamples {
				t.Run(sample.ID, func(t *testing.T) {
					want, wantErr := legacy.DecodeModelPage(t.Context(), sample.Operation, sample.Input)
					got, gotErr := draft.DecodeModelPage(t.Context(), sample.Operation, sample.Input)
					if wantErr != nil || gotErr != nil || !equalAgentMigrationValues(t, want, got) {
						t.Fatal(want, got, wantErr, gotErr)
					}
				})
			}
			for _, sample := range definition.Agent.Samples {
				t.Run(sample.ID, func(t *testing.T) {
					// These packages do not declare their real stream operations yet.
					if sample.Preferences.Stream {
						if _, err := draft.BuildAgentParameters(t.Context(), sample.Preferences); err == nil {
							t.Fatal("undeclared streaming transport was accepted")
						}
						return
					}
					want, wantErr := legacy.BuildAgentParameters(t.Context(), sample.Preferences)
					got, gotErr := draft.BuildAgentParameters(t.Context(), sample.Preferences)
					if wantErr != nil || gotErr != nil || !reflect.DeepEqual(want, got) {
						t.Fatal(want, got, wantErr, gotErr)
					}
					request := p.Request{SchemaVersion: legacy.CoreVersion(), Source: p.AgentIdentity(), Model: p.StringValue(sample.Model), Content: []p.Node{
						{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}},
						{Kind: p.ToolResultNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Payload: testValue(t, `{"n":9007199254740993,"empty":null}`), Source: &p.Provenance{Protocol: p.AgentIdentity()}},
					}}
					before, _ := p.EncodeValue(request)
					oldRequest, wantErr := legacy.BuildAgentRequest(t.Context(), request, sample.Preferences)
					request.SchemaVersion = draft.CoreVersion()
					newRequest, gotErr := draft.BuildAgentRequest(t.Context(), request, sample.Preferences)
					request.SchemaVersion = legacy.CoreVersion()
					after, _ := p.EncodeValue(request)
					if wantErr != nil || gotErr != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("Agent preparation failed or mutated history", wantErr, gotErr)
					}
					newRequest.SchemaVersion = oldRequest.SchemaVersion
					if !equalAgentMigrationValues(t, oldRequest, newRequest) {
						t.Fatalf("Agent preparation changed: old=%+v new=%+v", oldRequest, newRequest)
					}
					// The legacy model policy supplies this renderer comparison's
					// input. Its declaration migration is a separate pending boundary.
					route := p.ConversionContext{Source: legacy.Identity(), Target: legacy.Identity(), Model: sample.Model}
					oldRequest, wantErr = projection.Request(t.Context(), oldRequest, route, nil)
					newRequest, gotErr = projection.Request(t.Context(), newRequest, route, nil)
					if wantErr != nil || gotErr != nil {
						t.Fatal("model projection fixture failed", wantErr, gotErr)
					}
					newRequest.SchemaVersion = draft.CoreVersion()
					options := p.EvaluationContext{Scope: p.Scope{Model: sample.Model}, Values: p.Object{"model": p.StringValue(sample.Model)}}
					oldWire, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
					newWire, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
					if wantErr != nil || gotErr != nil || !bytes.Equal(oldWire, newWire) {
						t.Fatalf("Agent request changed: old=%s new=%s errors=%v / %v", oldWire, newWire, wantErr, gotErr)
					}
				})
			}
		})
	}
}

func equalAgentMigrationValues(t *testing.T, left, right any) bool {
	t.Helper()
	decode := func(input any) any {
		t.Helper()
		value, err := p.EncodeValue(input)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := value.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	return reflect.DeepEqual(decode(left), decode(right))
}
