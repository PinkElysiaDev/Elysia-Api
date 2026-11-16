package protocol

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"testing"
)

func hotPathChat(t *testing.T) *Compiled {
	t.Helper()
	spec, err := os.ReadFile("builtin/packages/openai-chat/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("builtin/packages/openai-chat/mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(DefaultLimits(), []string{featureWebSocketTransport, featureAsyncTasks})
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(spec, mapping)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func ownerOnlyFixture(compiled *Compiled) *Compiled {
	fixture := *compiled
	plan := *compiled.packagePlan
	plan.effects = maps.Clone(plan.effects)
	var effects []compiledSemanticEffect
	for _, effect := range plan.effects[EncodeRequest] {
		if isOwnerEffectScope(effect.scope) {
			effects = append(effects, effect)
		}
	}
	plan.effects[EncodeRequest] = effects
	fixture.packagePlan = &plan
	return &fixture
}

// The pre-fusion effect-major implementation remains the diagnostic oracle.
func legacyOwnerEffects(compiled *Compiled, state evaluation, limits Limits) error {
	state.budget = &evaluationBudget{limits: limits, stepLimit: limits.Nodes}
	for _, effect := range compiled.packagePlan.effects[EncodeRequest] {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		if err := compiled.checkOwnerEffect(EncodeRequest, state, effect, nil); err != nil {
			return err
		}
	}
	return nil
}

func assertSameHotPathError(t *testing.T, got, want error) {
	t.Helper()
	if reflect.TypeOf(got) != reflect.TypeOf(want) || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("error changed: %T %v / %T %v", got, got, want, want)
	}
	var left, right *ConversionError
	if errors.As(got, &left) != errors.As(want, &right) {
		t.Fatal("error classification changed")
	}
	if left != nil && !reflect.DeepEqual(left.Issues, right.Issues) {
		t.Fatalf("issues changed: %#v / %#v", left.Issues, right.Issues)
	}
}

func TestFusedOwnerEffectsKeepShippedDiagnostics(t *testing.T) {
	compiled := ownerOnlyFixture(hotPathChat(t))
	if len(compiled.packagePlan.effects[EncodeRequest]) != 5 {
		t.Fatal("fixture must cover every shipped owner category")
	}
	for _, test := range []struct {
		name, path, stage string
		nodes             []Node
	}{
		{"reasoning-shape-target", "/content/0/children/0/reasoningForm", "evaluate", []Node{{Kind: MessageNode, Children: []Node{{Kind: ReasoningNode, ReasoningForm: SummaryReasoning}}}}},
		{"metadata-target", "/content/0/children/0/metadata/0", "evaluate", []Node{{Kind: MessageNode, Children: []Node{{Kind: TextNode, Metadata: []ResponseMetadata{{Codec: "foreign", Name: "label", Value: StringValue("retained")}}}}}}},
		{"node-extension-target", "/content/0/children/0/attributes", "evaluate", []Node{{Kind: MessageNode, Children: []Node{{Kind: TextNode, Attributes: Object{"wire:foreign": StringValue("retained")}}}}}},
		{"request-child-order", "/content/0/children", "wire", []Node{{Kind: MessageNode, Children: []Node{{Kind: RefusalNode}, {Kind: TextNode}}}}},
		{"request-node-identity", "/content/0/children/0", "evaluate", []Node{{Kind: MessageNode, Children: []Node{{Kind: TextNode, ID: StringValue("retained")}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := &Request{SchemaVersion: ProtocolCoreVersion, Content: test.nodes}
			for _, typed := range []bool{false, true} {
				state := evaluation{ctx: t.Context(), coreDocument: request}
				if !typed {
					state.input = fixtureValue(t, request)
					var err error
					state.coreDocument, err = state.readCoreDocument(EncodeRequest)
					if err != nil {
						t.Fatal(err)
					}
				}
				want := legacyOwnerEffects(compiled, state, DefaultLimits())
				got := compiled.checkPackageEffects(EncodeRequest, state, DefaultLimits(), nil, nil)
				assertSameHotPathError(t, got, want)
				var failure *ConversionError
				if !errors.As(got, &failure) || len(failure.Issues) != 1 {
					t.Fatal("missing rejection", got)
				}
				issue := failure.Issues[0]
				if issue.RuleID != test.name || issue.Code != UnsupportedCapability || issue.Path != test.path || issue.Stage != test.stage || issue.Suggestion != "" {
					t.Fatalf("diagnostic changed: %#v", issue)
				}
			}
		})
	}
}

func TestFusedOwnerEffectsKeepInterveningEffectPrecedence(t *testing.T) {
	compiled := ownerOnlyFixture(hotPathChat(t))
	effects := compiled.packagePlan.effects[EncodeRequest]
	intervening := compiledSemanticEffect{id: "intervening", kind: "reject", reason: "original intervening rejection", path: "/parameters"}
	combined := append([]compiledSemanticEffect{effects[0], intervening}, effects[1:]...)
	compiled.packagePlan.effects[EncodeRequest] = combined
	state := evaluation{ctx: t.Context(), coreDocument: &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: TextNode, Metadata: []ResponseMetadata{{Codec: "foreign", Value: StringValue("retained")}}}}}}
	got := compiled.checkPackageEffects(EncodeRequest, state, DefaultLimits(), nil, nil)
	want := &ConversionError{Issues: []ConversionIssue{compiled.packageEffectIssue(EncodeRequest, intervening, intervening.reason)}}
	assertSameHotPathError(t, got, want)
}

func TestFusedOwnerEffectsKeepDeclarationAndBudgetPrecedence(t *testing.T) {
	compiled := ownerOnlyFixture(hotPathChat(t))
	for _, nodes := range [][]Node{
		{{Kind: TextNode, ID: StringValue("early-owner")}, {Kind: ReasoningNode, ReasoningForm: SummaryReasoning}},
		{{Kind: TextNode, Attributes: Object{"wire:foreign": StringValue("early-owner")}}, {Kind: TextNode, Metadata: []ResponseMetadata{{Codec: "foreign", Path: "/source/label", Value: StringValue("later-owner")}}}},
		{{Kind: MessageNode, Children: []Node{{Kind: TextNode, Payload: StringValue("valid")}, {Kind: TextNode, Payload: StringValue("also valid")}}}},
	} {
		state := evaluation{ctx: t.Context(), coreDocument: &Request{SchemaVersion: ProtocolCoreVersion, Content: nodes}}
		for _, steps := range []int{1, 2, 3, 5, 10, 20, 32, 64, 128, 256, 512, 100000} {
			limits := DefaultLimits()
			limits.Nodes = steps
			assertSameHotPathError(t, compiled.checkPackageEffects(EncodeRequest, state, limits, nil, nil), legacyOwnerEffects(compiled, state, limits))
		}
	}
}
