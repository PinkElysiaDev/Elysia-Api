package protocol

import "testing"

func associationRangePackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	isMessage := Expression{Op: "equal", Items: []Expression{readExpression("item", "/kind"), literalExpression(MessageNode)}}
	nodes := Expression{Op: "flatmap", Source: expressionPointer(readExpression("input", "/content")), Body: &Expression{Op: "if", When: &isMessage,
		Then: expressionPointer(readExpression("item", "/children")), Otherwise: &Expression{Op: "array", Items: []Expression{readExpression("item", "")}}}}
	declaration := ProtocolDeliveryRange{Name: "node-references", Measure: Expression{Op: "utf16_length", Source: expressionPointer(readExpression("input", ""))},
		Texts: Expression{Op: "map", Source: &nodes, Body: expressionPointer(objectExpression(map[string]Expression{
			"key": readExpression("item", "/association"), "text": readExpression("item", "/payload"), "mode": literalExpression("snapshot"), "path": literalExpression("/content"),
		}))},
		Ranges: Expression{Op: "flatmap", Source: &nodes, Body: &Expression{Op: "let", Name: "owner", Source: expressionPointer(readExpression("item", "/association")),
			Body: &Expression{Op: "map", Source: expressionPointer(readExpression("item", "/metadata/0/references")), Body: expressionPointer(objectExpression(map[string]Expression{
				"key": {Op: "read", From: "binding", Name: "owner"}, "id": readExpression("item", "/url"),
				"start": readExpression("item", "/start"), "end": readExpression("item", "/end"), "path": literalExpression("/reference"),
			}))}}},
	}
	mapping := document.Mapping.Directions[EncodeResponse]
	mapping.SemanticRanges = []ProtocolDeliveryRange{declaration}
	document.Mapping.Directions[EncodeResponse] = mapping
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "generated-identities", Order: 20, Enabled: true, Phase: ConversionResponse,
		Steps: []ProtocolControlStep{
			{Set: map[string]Expression{"/id": literalExpression("target-response")}},
			{Each: &ProtocolControlEach{Select: "node", PathBinding: "node-path", Steps: []ProtocolControlStep{{
				Set: map[string]Expression{"/id": {Op: "read", From: "binding", Name: "node-path"}},
			}}}},
		}}}
	return document
}

func referenceNode(t *testing.T, name string) Node {
	t.Helper()
	references := fixtureValue(t, []map[string]any{{"url": "https://example.test/" + name, "title": name, "start": 0, "end": 4}})
	return Node{Kind: TextNode, Payload: StringValue("same"), Metadata: []ResponseMetadata{{Meaning: "url-references", Name: "links", Codec: "source", Location: "content",
		Path: "/original/" + name, Value: references, References: references}}}
}

func TestRangeAssociationsSurviveGeneratedIDsAndMoves(t *testing.T) {
	for _, layout := range []string{"none", "stable_partition", "combine_children"} {
		t.Run(layout, func(t *testing.T) {
			document := associationRangePackage(t)
			response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{referenceNode(t, "first"), referenceNode(t, "second")}}
			if layout != "none" {
				selection := literalExpression(true)
				if layout == "stable_partition" {
					selection = Expression{Op: "equal", Items: []Expression{readExpression("input", "/metadata/0/references/0/title"), literalExpression("second")}}
				} else {
					response.Content = []Node{{Kind: MessageNode, Children: response.Content[:1]}, {Kind: MessageNode, Children: response.Content[1:]}}
				}
				document.Mapping.ConversionDefaults = append([]ProtocolConversionDefault{{ID: "move-owners", Order: 10, Enabled: true, Phase: ConversionResponse,
					Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{Operation: layout, Path: "/content", When: selection,
						Steps: []ProtocolControlStep{{Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Reason: literalExpression("target moves content owners")}}}}}}}}, document.Mapping.ConversionDefaults...)
			}
			compiled := compilePackageForTest(t, document)
			conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			before := fixtureValue(t, response)
			route := ConversionContext{Source: Identity{DefinitionID: "source"}, Target: compiled.identity}
			projected, err := conversion.Response(t.Context(), response, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.ValidateResponseReferences(t.Context(), projected); err != nil {
				t.Fatal("target identity or container layout severed original reference ownership", err)
			}
			if !equalValues(before, fixtureValue(t, response)) || response.Content[0].rangeAssociation != "" {
				t.Fatal("binding proof mutated the source")
			}
			encoded := fixtureValue(t, projected)
			if value, _ := readValuePointer(encoded, []string{"content", "0", "association"}); !value.IsZero() {
				t.Fatal("private association escaped into public core JSON")
			}
		})
	}
}

func TestRangeAssociationsRejectDuplicateTextCitationReassignment(t *testing.T) {
	document := associationRangePackage(t)
	compiled := compilePackageForTest(t, document)
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{referenceNode(t, "first"), referenceNode(t, "second")}}
	defaults := DefaultConversionPolicy(compiled, compiled)
	route := ConversionContext{Source: Identity{DefinitionID: "source"}, Target: compiled.identity}
	for _, isSwapped := range []bool{false, true} {
		policy := defaults
		policy.Rules = append([]ConversionRule(nil), defaults.Rules...)
		if isSwapped {
			for index := range response.Content {
				path := "/content/0/metadata"
				if index == 1 {
					path = "/content/1/metadata"
				}
				policy.Rules = append(policy.Rules, ConversionRule{ID: "move-reference-" + string(rune('a'+index)), Order: 100 + index, Enabled: true, Phase: ConversionResponse,
					Action: "set", Path: path, Value: fixtureValue(t, response.Content[1-index].Metadata)})
			}
		} else {
			policy.Rules = append(policy.Rules, ConversionRule{ID: "ordinary-output-edit", Order: 100, Enabled: true, Phase: ConversionResponse,
				Action: "set", Path: "/model", Value: StringValue("edited-model")})
		}
		conversion, err := ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Response(t.Context(), response, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = compiled.ValidateResponseReferences(t.Context(), projected)
		if (err != nil) != isSwapped {
			t.Fatal("identical text granted another owner's reference proof", isSwapped, err)
		}
	}
}

func TestRangeAssociationProjectionIsReadOnly(t *testing.T) {
	document := associationRangePackage(t)
	for _, path := range []string{"/association", "/source", "/native"} {
		invalid := document
		invalid.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "forge-proof", Order: 10, Enabled: true, Phase: ConversionResponse,
			Steps: []ProtocolControlStep{{Each: &ProtocolControlEach{Select: "node", Steps: []ProtocolControlStep{{Set: map[string]Expression{path: literalExpression("forged")}}}}}}}}
		compiler, _ := NewCompiler(DefaultLimits(), nil)
		if compiled, issues := compiler.CompilePackage(fixtureValue(t, invalid).Bytes()); compiled != nil || IssuesError(issues) == nil {
			t.Fatal("authored controls created a private owner receipt", path)
		}
	}
}

func TestRangeAssociationsRequireExistingOwnersAfterJSONReorder(t *testing.T) {
	compiled := compilePackageForTest(t, associationRangePackage(t))
	defaults, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: Identity{DefinitionID: "source"}, Target: compiled.identity}
	response, err := defaults.Response(t.Context(), &Response{SchemaVersion: ProtocolCoreVersion,
		Content: []Node{referenceNode(t, "first"), referenceNode(t, "second")}}, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, isRenamed := range []bool{false, true} {
		content := []Node{response.Content[1], response.Content[0]}
		if isRenamed {
			content[0].ID = StringValue("unproven")
		}
		conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "reorder", Rules: []ConversionRule{{
			ID: "reorder-content", Order: 1, Enabled: true, Phase: ConversionResponse, Action: "set", Path: "/content", Value: fixtureValue(t, content),
		}}})
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Response(t.Context(), response, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.ValidateResponseReferences(t.Context(), projected); (err != nil) != isRenamed {
			t.Fatal("JSON reorder invented an owner or discarded an existing proof", isRenamed, err)
		}
	}
}
