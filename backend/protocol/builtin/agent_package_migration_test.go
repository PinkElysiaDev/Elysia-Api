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
			declaredProjection, err := p.ResolveConversion(p.DefaultConversionPolicy(draft, draft))
			if err != nil {
				t.Fatal(err)
			}
			document, _ := draft.Package()
			if !draft.HasAgentPolicy() || len(document.Mapping.Agent.Samples) != len(definition.Agent.Samples) {
				t.Fatal("declared Agent evidence changed")
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
			for _, sample := range document.Mapping.Agent.Samples {
				t.Run(sample.ID, func(t *testing.T) {
					if sample.Preferences.Stream {
						hasStream := false
						for _, operation := range draft.Operations() {
							hasStream = hasStream || operation.Kind == "generate" && (operation.Transport == p.SSE || operation.Transport == p.NDJSON)
						}
						if !hasStream {
							if _, err := draft.BuildAgentParameters(t.Context(), sample.Preferences); err == nil {
								t.Fatal("undeclared streaming transport was accepted")
							}
							return
						}
					}
					want, wantErr := legacy.BuildAgentParameters(t.Context(), sample.Preferences)
					got, gotErr := draft.BuildAgentParameters(t.Context(), sample.Preferences)
					if stored := want["store"]; !stored.IsZero() && stored.JSONString() != "false" {
						t.Fatal("historical Agent fixture must explicitly disable storage", stored)
					}
					delete(want, "store")
					delete(want, "responses_include")
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
					expected := oldRequest.Clone()
					delete(expected.Parameters, "store")
					expected.ClientOutput.ResponsesStorage = &p.ResponsesStorageIntent{}
					migrateFixtureInclude(t, expected)
					declaredOutput, err := p.EncodeValue(newRequest.ClientOutput)
					if err != nil || !equalAgentMigrationValues(t, declaredOutput, sample.ExpectedClientOutput) {
						t.Fatal("Agent did not produce its complete declared client intent", newRequest.ClientOutput, err)
					}
					if !equalAgentMigrationValues(t, expected, newRequest) {
						t.Fatalf("Agent preparation changed: old=%+v new=%+v", oldRequest, newRequest)
					}
					route := p.ConversionContext{Source: legacy.Identity(), Target: legacy.Identity(), Model: sample.Model}
					oldRequest, wantErr = projection.Request(t.Context(), oldRequest, route, nil)
					newRequest.SchemaVersion = draft.CoreVersion()
					route.Source, route.Target = draft.Identity(), draft.Identity()
					newRequest, gotErr = declaredProjection.Request(t.Context(), newRequest, route, nil)
					if wantErr != nil || gotErr != nil {
						t.Fatal("model projection fixture failed", wantErr, gotErr)
					}
					newRequest.SchemaVersion = draft.CoreVersion()
					options := p.EvaluationContext{Scope: p.Scope{Model: sample.Model}, Values: p.Object{"model": p.StringValue(sample.Model)}}
					oldWire, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
					options.ClientOutput = newRequest.ClientOutput
					newWire, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
					if wantErr != nil || gotErr != nil || !bytes.Equal(oldWire, newWire) {
						t.Fatalf("Agent request changed: old=%s new=%s errors=%v / %v", oldWire, newWire, wantErr, gotErr)
					}
					oldDecoded, wantErr := legacy.DecodeRequest(t.Context(), oldWire, options)
					newDecoded, gotErr := draft.DecodeRequest(t.Context(), newWire, options)
					if wantErr != nil || gotErr != nil {
						t.Fatal("Agent wire cannot be decoded", wantErr, gotErr)
					}
					migrateFixtureStorage(t, oldDecoded, legacy.Codec(p.DecodeRequest))
					migrateFixtureInclude(t, oldDecoded)
					if !equalAgentMigrationValues(t, oldDecoded.ClientOutput, newDecoded.ClientOutput) {
						t.Fatal("Agent wire changed client storage intent", oldDecoded.ClientOutput, newDecoded.ClientOutput)
					}
					if !equalAgentMigrationValues(t, oldDecoded.Reasoning, newDecoded.Reasoning) || !equalAgentMigrationValues(t, oldDecoded.Parameters, newDecoded.Parameters) {
						t.Fatal("Agent controls changed through the declared wire", oldDecoded.Reasoning, newDecoded.Reasoning, oldDecoded.Parameters, newDecoded.Parameters)
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
