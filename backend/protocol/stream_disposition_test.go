package protocol

import "testing"

func dispositionPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	document.Mapping.Native.Preserve = false
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "layout", Order: 310, Enabled: true, Phase: ConversionEvent,
		StreamDisposition: &ProtocolStreamDisposition{Kind: "degrade", Path: "/itemId", Reason: "combine declared item boundaries"}}}
	mapping := document.Mapping.Directions[EncodeEvent]
	require := Expression{Op: "require_disposition", Name: "layout", Source: expressionPointer(literalExpression("/itemId"))}
	position := Expression{Op: "track_position", Name: "text", Source: expressionPointer(literalExpression("item")), Body: expressionPointer(literalExpression(1))}
	output := mapping.Program
	mapping.Program = Expression{Op: "let", Name: "position", Source: &position, Body: &Expression{Op: "let", Name: "loss", Source: &require, Body: &output}}
	document.Mapping.Directions[EncodeEvent] = mapping
	return document
}

func TestStreamDispositionTransaction(t *testing.T) {
	compiled := compilePackageForTest(t, dispositionPackage(t))
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: SSE}
	options := (EvaluationContext{State: NewEvaluationState(), Diagnostics: &DiagnosticSink{}}).WithConversion(conversion, route)
	defer options.State.Release()
	event := Event{SchemaVersion: compiled.CoreVersion(), Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("i"), Delta: StringValue("x")}
	if _, err := compiled.EncodeFrames(t.Context(), event, options); err == nil {
		t.Fatal("wire contract accepted a missing sequence")
	}
	if len(options.State.packageStates) != 0 || len(options.Diagnostics.Issues()) != 0 {
		t.Fatal("failed output committed a position or diagnostic")
	}
	event.Sequence = coreInteger(0)
	for range 2 {
		if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
			t.Fatal(err)
		}
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: EncodeEvent}]
	if retained.primitives.positionEnds["text"] != 2 || len(options.Diagnostics.Issues()) != 1 || options.Diagnostics.Issues()[0].RuleID != "layout" {
		t.Fatal("failed event advanced state or repeated diagnostics", options.Diagnostics.Issues(), retained.primitives.positionEnds)
	}
	if conversion.HasPhase(ConversionEvent) {
		t.Fatal("deferred disposition entered the ordinary event pass")
	}
}

func TestStreamDispositionRequiresResolvedAuthorization(t *testing.T) {
	compiled := compilePackageForTest(t, dispositionPackage(t))
	for _, name := range []string{"unbound", "strict", "disabled", "replaced", "unmatched", "wrong-target", "reject"} {
		t.Run(name, func(t *testing.T) {
			policy := DefaultConversionPolicy(compiled, compiled)
			route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: SSE}
			switch name {
			case "strict":
				policy.Mode = "strict"
			case "disabled":
				policy.Rules[0].Enabled = false
			case "replaced":
				policy.Rules[0].Action, policy.Rules[0].Value = "warn", Value{}
			case "unmatched":
				policy.Rules[0].Match.Model = "other-model"
			case "wrong-target":
				route.Target.DefinitionID = "other-target"
			case "reject":
				var option streamDispositionOptions
				if err := policy.Rules[0].Value.Decode(&option); err != nil {
					t.Fatal(err)
				}
				option.Disposition.Kind = "reject"
				policy.Rules[0].Value = fixtureValue(t, option)
			}
			conversion, err := ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			options := EvaluationContext{State: NewEvaluationState(), Diagnostics: &DiagnosticSink{}}
			defer options.State.Release()
			if name != "unbound" {
				options = options.WithConversion(conversion, route)
			}
			event := Event{SchemaVersion: compiled.CoreVersion(), Type: ItemDelta, ResponseID: StringValue("r"), Sequence: coreInteger(0), ItemID: StringValue("i"), Delta: StringValue("x")}
			if _, err := compiled.EncodeFrames(t.Context(), event, options); err == nil {
				t.Fatal("unresolved loss was authorized")
			}
			if len(options.State.packageStates) != 0 || len(options.Diagnostics.Issues()) != 0 {
				t.Fatal("rejected disposition committed state or warning")
			}
		})
	}
}

func TestStreamDispositionCompileBoundary(t *testing.T) {
	for _, name := range []string{"undeclared", "decoder", "non-route-match", "mixed-rule"} {
		t.Run(name, func(t *testing.T) {
			document := dispositionPackage(t)
			switch name {
			case "undeclared":
				document.Mapping.ConversionDefaults = nil
			case "decoder":
				mapping := document.Mapping.Directions[DecodeEvent]
				mapping.Program = *document.Mapping.Directions[EncodeEvent].Program.Body.Source
				document.Mapping.Directions[DecodeEvent] = mapping
			case "non-route-match":
				yes := true
				document.Mapping.ConversionDefaults[0].Match.Present = &yes
			case "mixed-rule":
				document.Mapping.ConversionDefaults[0].Use = "request_projection"
			}
			compiler, err := NewCompiler(DefaultLimits(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			value := fixtureValue(t, document)
			if _, issues := compiler.Compile(value.Bytes()); IssuesError(issues) == nil {
				t.Fatal("invalid disposition scope compiled")
			}
		})
	}
}

func TestStreamDispositionNativeObservation(t *testing.T) {
	document := dispositionPackage(t)
	document.Mapping.Native.Preserve = true
	entry := document.Mapping.Directions[EncodeEvent]
	entry.IsFrameBatch, entry.NativeReplay = true, "original_frame"
	entry.Observe = &Expression{Op: "require_disposition", Name: "layout", Source: expressionPointer(literalExpression("/itemId"))}
	entry.Program = Expression{Op: "array", Items: []Expression{entry.Program}}
	entry.Effects[0].Target = "/0/response_id"
	document.Mapping.Directions[EncodeEvent] = entry
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState(), Diagnostics: &DiagnosticSink{}}
	defer options.State.Release()
	wire := packageStreamFrame(ResponseStarted, 0, `,"extension":9007199254740993`)
	frame, err := compiled.DecodeFrame(t.Context(), wire, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.EncodeFrame(t.Context(), frame, options); err == nil {
		t.Fatal("native replay bypassed required disposition")
	}
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	options = options.WithConversion(conversion, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()})
	frames, err := compiled.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(frames) != 1 || frames[0] != wire || len(options.Diagnostics.Issues()) != 1 {
		t.Fatal("authorized native observation changed or duplicated the original frame", frames, err, options.Diagnostics.Issues())
	}
}

func TestStreamDispositionMappingPreview(t *testing.T) {
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{compiler: compiler}
	input := PreviewInput{Mode: "mapping", Direction: EncodeEvent, Definition: fixtureValue(t, dispositionPackage(t)),
		Input: fixtureValue(t, Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ResponseID: StringValue("r"), Sequence: coreInteger(0), ItemID: StringValue("i"), Delta: StringValue("x")})}
	result := service.PreviewWorkflow(t.Context(), input)
	if IssuesError(result.Issues) != nil || result.Output.IsZero() || len(result.Issues) != 1 || result.Issues[0].RuleID != "layout" {
		t.Fatal("preview did not bind declared target dispositions", result)
	}
	input.ConversionPolicy = &ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "strict-preview", Mode: "strict"}
	result = service.PreviewWorkflow(t.Context(), input)
	if IssuesError(result.Issues) == nil || !result.Output.IsZero() || result.Issues[0].RuleID != "layout" {
		t.Fatal("preview ignored the resolved strict policy", result)
	}
}
