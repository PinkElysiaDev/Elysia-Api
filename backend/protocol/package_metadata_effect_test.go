package protocol

import (
	"context"
	"errors"
	"testing"
)

func metadataRejection() ProtocolSemanticEffect {
	return ProtocolSemanticEffect{ID: "unrepresented-metadata", Kind: "reject", Scope: metadataScope,
		When:   &Expression{Op: "not", Source: &Expression{Op: "equal", Items: []Expression{readExpression("input", "/codec"), literalExpression("local")}}},
		Reason: "metadata has no local representation", Diagnostic: &RejectDiagnostic{Code: UnsupportedCapability, Stage: "evaluate"}}
}

func TestPackageMetadataRejectionVisitsOwners(t *testing.T) {
	document := textProtocolPackage(t)
	for _, direction := range []Direction{EncodeRequest, EncodeResponse} {
		mapping := document.Mapping.Directions[direction]
		mapping.Effects = append(mapping.Effects, metadataRejection())
		document.Mapping.Directions[direction] = mapping
	}
	compiled := compilePackageForTest(t, document)
	effects := compiled.packagePlan.effects[EncodeResponse]
	effect := effects[len(effects)-1]
	metadata := []ResponseMetadata{{Codec: "foreign", Name: "label", Value: StringValue("retained")}}
	nodes := []Node{{Kind: MessageNode, Children: []Node{{Kind: ReasoningNode, ReasoningContent: []Node{{Kind: TextNode, Metadata: metadata}}}}}}
	for _, fixture := range []struct {
		name      string
		direction Direction
		input     any
		path      string
	}{
		{"request-history", EncodeRequest, &Request{SchemaVersion: ProtocolCoreVersion, Content: nodes}, "/content/0/children/0/reasoningContent/0/metadata/0"},
		{"response-root", EncodeResponse, &Response{SchemaVersion: ProtocolCoreVersion, Metadata: metadata}, "/metadata/0"},
		{"response-content", EncodeResponse, &Response{SchemaVersion: ProtocolCoreVersion, Content: nodes}, "/content/0/children/0/reasoningContent/0/metadata/0"},
		{"event-root", EncodeEvent, &Event{SchemaVersion: ProtocolCoreVersion, Metadata: metadata}, "/metadata/0"},
		{"event-item", EncodeEvent, &Event{SchemaVersion: ProtocolCoreVersion, Item: &Node{Metadata: metadata}}, "/item/metadata/0"},
		{"event-response", EncodeEvent, &Event{SchemaVersion: ProtocolCoreVersion, Response: &Response{Content: nodes}}, "/response/content/0/children/0/reasoningContent/0/metadata/0"},
		{"event-request", EncodeEvent, &Event{SchemaVersion: ProtocolCoreVersion, Request: &Request{Content: nodes}}, "/request/content/0/children/0/reasoningContent/0/metadata/0"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			original, err := EncodeValue(fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			for _, isTyped := range []bool{false, true} {
				state := evaluation{ctx: t.Context(), input: original, budget: &evaluationBudget{limits: DefaultLimits()}}
				if isTyped {
					state.coreDocument = fixture.input
				} else {
					state.coreDocument, err = state.readCoreDocument(fixture.direction)
					if err != nil {
						t.Fatal(err)
					}
				}
				err := compiled.checkOwnerEffect(fixture.direction, state, effect, nil)
				var failure *ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 {
					t.Fatalf("typed=%v: %v", isTyped, err)
				}
				issue := failure.Issues[0]
				if issue.Path != fixture.path || issue.Direction != fixture.direction || issue.Code != UnsupportedCapability || issue.RuleID != "unrepresented-metadata" {
					t.Fatal(issue)
				}
			}
			after, _ := EncodeValue(fixture.input)
			if !equalValues(original, after) {
				t.Fatal("read-only rejection changed the core document")
			}
		})
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Metadata: metadata,
		Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}}
	response.Metadata[0].Path = "/original/label"
	_, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Path != "/original/label" {
		t.Fatal("encode bypassed the declared metadata check", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.EncodeResponse(ctx, response, EvaluationContext{}); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}

func TestPackageMetadataRejectionDeclaration(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolSemanticEffect)
	}{
		{"emit", func(effect *ProtocolSemanticEffect) { effect.Kind = "emit" }},
		{"absent-condition", func(effect *ProtocolSemanticEffect) { effect.When = nil }},
		{"nonlocal-path", func(effect *ProtocolSemanticEffect) { effect.Path = "/metadata" }},
		{"default", func(effect *ProtocolSemanticEffect) { effect.Default = literalExpression(nil).Value }},
		{"context", func(effect *ProtocolSemanticEffect) {
			effect.When = expressionPointer(readExpression("context", "/approved"))
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			mapping := document.Mapping.Directions[EncodeResponse]
			effect := metadataRejection()
			fixture.edit(&effect)
			mapping.Effects = append(mapping.Effects, effect)
			document.Mapping.Directions[EncodeResponse] = mapping
			if err := document.Validate(DefaultLimits()); err == nil {
				t.Fatal("invalid metadata scope compiled")
			}
		})
	}
	if err := validateOwnerEffect(DecodeResponse, metadataRejection()); err == nil {
		t.Fatal("wire decoder accepted a core owner selector")
	}
}

func TestPackageNodeRejectionUsesActualOwner(t *testing.T) {
	document := textProtocolPackage(t)
	effect := ProtocolSemanticEffect{ID: "foreign-node-field", Scope: nodeScope, Kind: "reject", Path: "/attributes",
		When:   &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/attributes/wire:foreign"))},
		Reason: "target does not represent this node extension"}
	mapping := document.Mapping.Directions[EncodeResponse]
	mapping.Effects = append(mapping.Effects, effect)
	document.Mapping.Directions[EncodeResponse] = mapping
	compiled := compilePackageForTest(t, document)
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{
		Kind: TextNode, Payload: StringValue("hello"), Attributes: Object{"wire:foreign": fixtureValue(t, map[string]any{"scope": "retained"})},
	}}}}}
	original := fixtureValue(t, response)
	_, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Path != "/content/0/children/0/attributes" || failure.Issues[0].RuleID != effect.ID {
		t.Fatal("typed node selection missed the nested owner", err)
	}
	_, issues := compiled.Execute(t.Context(), EncodeResponse, original, EvaluationContext{})
	if len(issues) != 1 || issues[0].Path != "/content/0/children/0/attributes" {
		t.Fatal("JSON node selection missed the nested owner", issues)
	}
	if !equalValues(original, fixtureValue(t, response)) {
		t.Fatal("read-only node rejection changed input")
	}
}
