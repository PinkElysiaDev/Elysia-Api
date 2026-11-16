package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func preludePackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	document.Mapping.Native.Preserve = false
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "opening-header", Order: 10, Enabled: true, Phase: ConversionEvent,
		Prelude: &ProtocolEventPrelude{When: literalExpression(true), Reason: "target requires an explicit opening header",
			Response: objectExpression(map[string]Expression{
				"id": readExpression("input", "/responseId"), "model": readExpression("context", "/model"),
				"attributes": objectExpression(map[string]Expression{"created_at": readExpression("runtime", "/operation/startedAt")}),
			})}}}
	return document
}

func resolvePrelude(t *testing.T, document ProtocolPackage) (*Compiled, *CompiledConversion, ConversionContext) {
	t.Helper()
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	options, err := (EvaluationContext{}).WithOperation("local-operation", 42)
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: Identity{DefinitionID: "source"}, Target: compiled.Identity(), Model: "requested-model", Transport: SSE}.WithOperation(options)
	return compiled, conversion, route
}

func TestEventPreludeRunsOnceWithTargetOwnership(t *testing.T) {
	_, conversion, route := resolvePrelude(t, preludePackage(t))
	state := NewConversionEventState(conversion, route)
	defer state.Release()
	event := Event{SchemaVersion: ProtocolCoreVersion, Source: route.Source, Type: ItemStarted, ResponseID: StringValue("r"), ItemID: StringValue("item"), Item: &Node{Kind: TextNode}}
	queued, err := state.PushContext(t.Context(), event)
	if err != nil || len(queued) != 2 || !state.Buffered {
		t.Fatal("declared prelude was not inserted before source content", queued, err)
	}
	header := queued[0]
	if header.Type != ResponseStarted || header.Source != route.Target || header.Response.Source != route.Target || !header.Response.ID.IsString("r") || !header.Response.Model.IsString("requested-model") || header.Response.Attributes["created_at"] != coreInteger(42) || header.Native != nil || header.Usage != nil || header.Item != nil {
		t.Fatal("target prelude manufactured provider facts or lost declared fields", header)
	}
	if queued[1].Source != event.Source || queued[1].Item != event.Item || event.Response != nil {
		t.Fatal("prelude changed the source event")
	}
	sink := &DiagnosticSink{}
	converted, err := conversion.Event(t.Context(), header, route, sink)
	issues := sink.Issues()
	if err != nil || converted.Response == nil || len(issues) != 1 || issues[0].Code != ConversionNormalized || issues[0].RuleID != "opening-header" || issues[0].PolicyHash != conversion.Hash || issues[0].PolicyRevision == "" || issues[0].Protocol != route.Target {
		t.Fatal("target-generated semantics lost their policy diagnostic", converted, issues, err)
	}
	event.Type, event.Item = ItemFinished, nil
	queued, err = state.Push(event)
	if err != nil || len(queued) != 1 || queued[0].Type != ItemFinished {
		t.Fatal("prelude ran twice", queued, err)
	}
	wrongRoute := route
	wrongRoute.Target.DefinitionID = "other-target"
	if _, err := conversion.Event(t.Context(), header, wrongRoute, nil); err == nil {
		t.Fatal("generated semantics crossed their target binding")
	}
}

func TestEventPreludeRespectsSourceLifecycleAndCancellation(t *testing.T) {
	_, conversion, route := resolvePrelude(t, preludePackage(t))
	for _, kind := range []EventType{ResponseStarted, OperationFailed, OperationCancelled} {
		t.Run(string(kind), func(t *testing.T) {
			state := NewConversionEventState(conversion, route)
			defer state.Release()
			first := Event{SchemaVersion: ProtocolCoreVersion, Type: kind}
			queued, err := state.Push(first)
			if err != nil || len(queued) != 1 || queued[0].Type != kind {
				t.Fatal("prelude altered an existing opening or terminal failure", queued, err)
			}
			queued, err = state.Push(Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished})
			if err != nil || len(queued) != 1 {
				t.Fatal("prelude reopened a started or failed stream", queued, err)
			}
		})
	}
	state := NewConversionEventState(conversion, route)
	defer state.Release()
	for _, kind := range []EventType{UsageUpdated, ControlUpdated, NativeEvent} {
		queued, err := state.Push(Event{SchemaVersion: ProtocolCoreVersion, Type: kind})
		if err != nil || len(queued) != 1 {
			t.Fatal("prelude promoted auxiliary or accounting observations into a response", queued, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r")}
	if _, err := state.PushContext(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled prelude evaluation succeeded", err)
	}
	if queued, err := state.Push(event); err != nil || len(queued) != 2 {
		t.Fatal("failed evaluation consumed the opening boundary", queued, err)
	}
}

func TestEventPreludeUsesEffectiveRuleAndRejectsContentFabrication(t *testing.T) {
	document := preludePackage(t)
	_, conversion, route := resolvePrelude(t, document)
	rule := conversion.Policy.Rules[0]
	rule.Enabled = false
	disabled, err := ResolveConversion(conversion.Policy, ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "binding", Rules: []ConversionRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	state := NewConversionEventState(disabled, route)
	defer state.Release()
	if queued, err := state.Push(Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished}); err != nil || len(queued) != 1 {
		t.Fatal("disabled target default still ran", queued, err)
	}
	for _, field := range []string{"content", "usage", "source", "native", "error"} {
		t.Run(field, func(t *testing.T) {
			document := preludePackage(t)
			document.Mapping.ConversionDefaults[0].Prelude.Response.Fields[field] = literalExpression(nil)
			_, conversion, route := resolvePrelude(t, document)
			state := NewConversionEventState(conversion, route)
			defer state.Release()
			if _, err := state.Push(Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished}); err == nil {
				t.Fatal("prelude accepted a provider-owned response field", field)
			}
		})
	}
}

func TestEventPreludeReloadPreservesBoundsAndReferences(t *testing.T) {
	document := preludePackage(t)
	document.Mapping.Expressions = map[string]Expression{"opening-model": readExpression("context", "/model")}
	document.Mapping.ConversionDefaults[0].Prelude.Response.Fields["model"] = Expression{Op: "ref", Ref: "opening-model"}
	_, original, route := resolvePrelude(t, document)
	limits := DefaultLimits()
	limits.BufferBytes = 512
	policy := original.Policy
	policy.Limits = &limits
	policy.Continuation = nil
	var saved ConversionPolicy
	if err := fixtureValue(t, policy).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	conversion, err := CompileConversion(saved)
	if err != nil {
		t.Fatal("persisted prelude references did not compile", err)
	}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r")}
	route.Model = strings.Repeat("x", semanticLimits(limits).BufferBytes+1)
	state := NewConversionEventState(conversion, route)
	defer state.Release()
	var limit *ResourceLimitError
	if queued, err := state.Push(event); len(queued) != 0 || !errors.As(err, &limit) {
		t.Fatal("generated header escaped the effective conversion budget", queued, err)
	}
	if state.hasResponseStarted || state.Buffered {
		t.Fatal("failed prelude changed stream state")
	}
	route.Model = "bounded"
	state = NewConversionEventState(conversion, route)
	defer state.Release()
	if queued, err := state.Push(event); err != nil || len(queued) != 2 || !queued[0].Response.Model.IsString("bounded") {
		t.Fatal("recompiled prelude lost its expression or owner", queued, err)
	}
}

func TestEventPreludeRejectsAmbiguousDeclarations(t *testing.T) {
	for _, fault := range []string{"phase", "steps", "condition", "reason", "runtime", "encoder"} {
		t.Run(fault, func(t *testing.T) {
			document := preludePackage(t)
			declaration := &document.Mapping.ConversionDefaults[0]
			switch fault {
			case "phase":
				declaration.Phase = ConversionResponse
			case "steps":
				declaration.Steps = []ProtocolControlStep{{Stop: true}}
			case "condition":
				declaration.Prelude.When = Expression{}
			case "reason":
				declaration.Prelude.Reason = ""
			case "runtime":
				declaration.Prelude.Response.Fields["id"] = readExpression("runtime", "/nativeFrame")
			case "encoder":
				delete(document.Mapping.Directions, EncodeEvent)
			}
			compiler, err := NewCompiler(DefaultLimits(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("ambiguous or unbound prelude compiled", issues)
			}
		})
	}
}

func TestEventPreludeCombinationComparesGeneratedFieldsAndContent(t *testing.T) {
	source := eventProtocolPackage(t)
	source.Spec.ID = "prelude-source"
	source.Mapping.Native.Preserve = false
	stream := source.Spec.Documents["provider-event"].Stream
	stream.Events[string(ItemStarted)] = append(ProtocolStreamEventSpec{{Action: "stream_start", Implicit: true}}, stream.Events[string(ItemStarted)]...)
	upstream := compilePackageForTest(t, source)
	sample := Sample{Direction: DecodeEvent, Sequence: true, Input: fixtureValue(t, []Value{
		packageStreamFrame(ItemStarted, 1, `,"item_id":"text"`),
		packageStreamFrame(ItemDelta, 2, `,"item_id":"text","text":"kept"`), packageStreamFrame(ItemFinished, 3, `,"item_id":"text"`), packageStreamFrame(ResponseFinished, 4, ""),
	})}
	for _, fault := range []string{"none", "model", "created_at", "content"} {
		t.Run(fault, func(t *testing.T) {
			target := preludePackage(t)
			target.Spec.ID = "prelude-target"
			for _, name := range []string{"provider-event", "client-event"} {
				contract := target.Spec.Documents[name]
				contract.Schema.Required = []string{"kind", "response_id"}
				contract.Stream.Sequence = ""
				target.Spec.Documents[name] = contract
			}
			encoder, decoder := target.Mapping.Directions[EncodeEvent], target.Mapping.Directions[DecodeEvent]
			encoder.Program.Fields["header"] = readExpression("input", "/response")
			decoder.Program.Fields["response"] = readExpression("input", "/header")
			if fault == "model" || fault == "created_at" {
				fields := map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "id": readExpression("input", "/response/id"),
					"model": readExpression("input", "/response/model"), "attributes": readExpression("input", "/response/attributes")}
				if fault == "model" {
					fields["model"] = literalExpression("wrong-model")
				} else {
					fields["attributes"] = objectExpression(map[string]Expression{"created_at": literalExpression(verificationStartedAt + 1)})
				}
				header := readExpression("input", "/response")
				hasHeader := Expression{Op: "exists", Source: &header}
				encoder.Program.Fields["header"] = Expression{Op: "if", When: &hasHeader, Then: expressionPointer(objectExpression(fields)), Otherwise: expressionPointer(Expression{Op: "omit"})}
			}
			if fault == "content" {
				delta := readExpression("input", "/delta")
				hasDelta := Expression{Op: "exists", Source: &delta}
				encoder.Program.Fields["text"] = Expression{Op: "if", When: &hasDelta, Then: expressionPointer(literalExpression("lost")), Otherwise: expressionPointer(Expression{Op: "omit"})}
			}
			target.Mapping.Directions[EncodeEvent], target.Mapping.Directions[DecodeEvent] = encoder, decoder
			ingress := compilePackageForTest(t, target)
			conversion, err := ResolveConversion(DefaultConversionPolicy(ingress, upstream))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), conversionVerificationKey{}, conversion)
			err = verifyEventCombination(ctx, ingress, upstream, sample)
			if fault == "none" && err != nil {
				t.Fatal("declared target header did not survive strict roundtrip", err)
			}
			if fault != "none" && !hasIssueCode(err, VerificationMismatch) {
				t.Fatal("combination did not detect changed declared semantics", fault, err)
			}
		})
	}
}
