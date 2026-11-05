package protocol

import "testing"

func TestResponseRangeOriginOwnership(t *testing.T) {
	original := &Response{SchemaVersion: ProtocolCoreVersion, ID: StringValue("response"), Content: []Node{{ID: StringValue("message"), Kind: MessageNode, Children: []Node{{Kind: TextNode, Payload: StringValue("original")}}}}}
	conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := conversion.Response(t.Context(), original, ConversionContext{}, nil)
	if err != nil || unchanged.rangeOrigin != nil {
		t.Fatal("unchanged response retained a full origin", err)
	}
	conversion, err = ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "rewrite", Rules: []ConversionRule{{ID: "text", Enabled: true, Phase: ConversionResponse, Match: ConversionMatch{NodeKind: TextNode}, Action: "set", Path: "/payload", Value: StringValue("modified")}}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := conversion.Response(t.Context(), original, ConversionContext{}, nil)
	if err != nil || changed.rangeOrigin == nil {
		t.Fatal("semantic rewrite lost its origin", err)
	}
	original.Content[0].Children[0].Payload = StringValue("caller mutation")
	if !changed.rangeOrigin.Content[0].Children[0].Payload.IsString("original") {
		t.Fatal("origin borrowed mutable containers")
	}
	again, err := conversion.Response(t.Context(), changed, ConversionContext{}, nil)
	if err != nil || again.rangeOrigin != changed.rangeOrigin {
		t.Fatal("successive edits replaced the original proof", err)
	}
}

func TestResponseRangeProjectionRejectsAuthority(t *testing.T) {
	for _, path := range []string{"/source", "/native", "/content/0/resources", "/content/0/children/0/source"} {
		document := deliveryRangeTestPackage(t)
		entry := document.Mapping.Directions[EncodeResponse]
		entry.SemanticRanges = entry.DeliveryRanges
		entry.SemanticRanges[0].Texts = readExpression("input", path)
		document.Mapping.Directions[EncodeResponse] = entry
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		if _, issues := compiler.Compile(fixtureValue(t, document).Bytes()); IssuesError(issues) == nil {
			t.Fatal("response selector acquired authority", path)
		}
	}
	response := &Response{Content: []Node{{Kind: MessageNode, Children: []Node{{Kind: TextNode, Payload: StringValue("text"), Source: &Provenance{Protocol: Identity{Family: "private"}}}}}}}
	output, err := readSemanticRangeResponse(response, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := readOutputPointer(output, []string{"content", "0", "children", "0", "source"}, &valueReader{})
	if err != nil || !value.IsZero() {
		t.Fatal("whole-object response read exposed authority", value, err)
	}
}

func TestCoreConsumerRetainsLateReferenceProof(t *testing.T) {
	compiled := compilePackageForTest(t, semanticRangeDocument(t))
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	original := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("original")}
	changed := original
	changed.Delta, changed.rangeOrigin = StringValue("modified"), captureEventRangeOrigin(original)
	if err := compiled.ValidateEventReferences(t.Context(), changed, options); err != nil {
		t.Fatal(err)
	}
	reference := original
	reference.Delta = StringValue("")
	reference.Metadata = []ResponseMetadata{{Name: "references", Value: parseTestValue(t, `[{"key":"text","id":"citation","start":0,"end":8,"path":"/reference"}]`)}}
	if err := compiled.ValidateEventReferences(t.Context(), reference, options); err == nil {
		t.Fatal("core collection forgot an earlier text rewrite")
	}
}
