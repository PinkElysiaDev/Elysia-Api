package protocol

import (
	"reflect"
	"testing"
)

func noCachePackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := verifiedTextPackage(t, "no-cache-target")
	read := func(path string) Expression { return readExpression("input", path) }
	equal := func(path string, value any) Expression {
		return Expression{Op: "equal", Items: []Expression{read(path), literalExpression(value)}}
	}
	absent := func(path string) Expression {
		return Expression{Op: "not", Source: &Expression{Op: "exists", Source: expressionPointer(read(path))}}
	}
	drop := Expression{Op: "all", Items: []Expression{
		equal("/kind", "breakpoint"), equal("/value", map[string]any{"type": "ephemeral"}), absent("/resource"),
		{Op: "any", Items: []Expression{absent("/ttl"), equal("/ttl", nil), equal("/ttl", "5m"), equal("/ttl", "1h")}},
	}}
	document.Mapping.Expressions = map[string]Expression{"known-cache-hint": drop}
	entry := document.Mapping.Directions[EncodeRequest]
	entry.Effects = append(entry.Effects,
		ProtocolSemanticEffect{ID: "empty-cache", Kind: "default", Scope: cacheIntentScope, Default: Value{raw: "null"}, When: expressionPointer(literalExpression(true)), Reason: "empty cache hint has no target effect"},
		ProtocolSemanticEffect{ID: "cache-hint", Kind: "drop", Scope: cacheIntentScope, Severity: SeverityInfo, When: &Expression{Op: "ref", Ref: "known-cache-hint"}, Reason: "target has no cache breakpoint representation"})
	document.Mapping.Directions[EncodeRequest] = entry
	return document
}

func cacheProjectionPair(t *testing.T) (*Compiled, *Compiled, *Request, ConversionContext) {
	t.Helper()
	source := compilePackageForTest(t, cacheOwnerPackage(t))
	target := compilePackageForTest(t, noCachePackage(t))
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	return source, target, request, ConversionContext{Source: source.Identity(), Target: target.Identity()}
}

func TestPackageCacheProjectionPolicyOrderAndDiagnostics(t *testing.T) {
	source, target, request, route := cacheProjectionPair(t)
	policy := DefaultConversionPolicy(source, target)
	if len(policy.Rules) != 1 || policy.Rules[0].ID != "request-projection" || policy.Rules[0].Order != 130 {
		t.Fatal("package invented another policy lane", policy.Rules)
	}
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	sink := &DiagnosticSink{}
	projected, err := conversion.Request(t.Context(), request, route, sink)
	if err != nil || len(projected.Cache) != 0 || len(request.Cache) != 1 {
		t.Fatal("projection mutated source or retained supported omission", err)
	}
	wire, err := target.EncodeRequest(t.Context(), projected, EvaluationContext{})
	if err != nil || string(wire) != `{"model":"m","prompt":"hello"}` {
		t.Fatal("target did not encode projected request", err, string(wire))
	}
	issues := sink.Issues()
	if len(issues) != 1 || issues[0].Code != ConversionDegraded || issues[0].Severity != SeverityInfo || issues[0].Fidelity != "lossy_compatible" || issues[0].RuleID != "request-projection" || issues[0].PolicyHash != conversion.Hash || issues[0].Evidence != "engine-default" || issues[0].PolicyRevision == "" {
		t.Fatal("projection lost policy evidence", issues)
	}
	strict := policy
	strict.Mode = "strict"
	strictConversion, err := ResolveConversion(strict)
	if err != nil {
		t.Fatal(err)
	}
	_, err = strictConversion.Request(t.Context(), request, route, &DiagnosticSink{})
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/cache/0")
	for _, enabled := range []bool{false, true} {
		rule := policy.Rules[0]
		rule.Enabled = enabled
		if enabled {
			rule.Action, rule.Value, rule.Reason = "warn", Value{}, "operator replaced projection"
		}
		overridden, err := ResolveConversion(policy, ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "operator", Rules: []ConversionRule{rule}})
		if err != nil {
			t.Fatal(err)
		}
		untouched, err := overridden.Request(t.Context(), request, route, &DiagnosticSink{})
		if err != nil || len(untouched.Cache) != 1 {
			t.Fatal("disabled or replaced projection still executed", err)
		}
		_, err = target.EncodeRequest(t.Context(), untouched, EvaluationContext{})
		if err == nil {
			t.Fatal("encoder silently repeated disabled projection")
		}
	}
	// Even a target declaring the coarse capability cannot bypass the remaining
	// disposition check by directly invoking its encoder or raw mapping.
	document := noCachePackage(t)
	document.Mapping.Capabilities[CacheBreakpointsCapability] = true
	coarse := compilePackageForTest(t, document)
	_, err = coarse.EncodeRequest(t.Context(), request, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/cache/0")
	_, diagnostics := coarse.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
	if IssuesError(diagnostics) == nil {
		t.Fatal("raw encoding repeated the projection")
	}
}

func TestPackageCacheProjectionDefaultsAndUnknowns(t *testing.T) {
	source, target, original, route := cacheProjectionPair(t)
	policy := DefaultConversionPolicy(source, target)
	policy.Mode = "strict"
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"key", "retention", "breakpoint"} {
		request := original.Clone()
		request.Cache = []CacheIntent{{Kind: kind, Location: "request", Value: Value{raw: "null"}}}
		sink := &DiagnosticSink{}
		projected, err := conversion.Request(t.Context(), request, route, sink)
		if err != nil || len(projected.Cache) != 0 || len(sink.Issues()) != 1 || sink.Issues()[0].Fidelity != "preserved" || sink.Issues()[0].Code != ConversionNormalized {
			t.Fatal("null default lost its bounded normalization", kind, err, sink.Issues())
		}
	}
	policy.Mode = "compatible"
	conversion, _ = ResolveConversion(policy)
	for _, intent := range []CacheIntent{
		{Kind: "breakpoint", Value: Value{raw: `{"type":"ephemeral","future":true}`}},
		{Kind: "breakpoint", Value: Value{raw: `{"type":"ephemeral"}`}, TTL: StringValue("2h")},
		{Kind: "retention", Location: "request", Value: StringValue("unknown")},
	} {
		request := original.Clone()
		request.Cache = []CacheIntent{intent}
		sink := &DiagnosticSink{}
		projected, err := conversion.Request(t.Context(), request, route, sink)
		if err != nil || !reflect.DeepEqual(projected.Cache, request.Cache) || len(sink.Issues()) != 0 {
			t.Fatal("undeclared cache variant was omitted", intent, err)
		}
		if _, err := target.EncodeRequest(t.Context(), projected, EvaluationContext{}); err == nil {
			t.Fatal("unmapped cache capability was accepted", intent)
		}
	}
	invalid := original.Clone()
	invalid.Cache = []CacheIntent{{Kind: "breakpoint", Value: Value{raw: "null"}, TTL: Value{raw: "null"}}}
	if _, err := conversion.Request(t.Context(), invalid, route, &DiagnosticSink{}); err == nil {
		t.Fatal("default hid an invalid null policy with TTL")
	}
}

func TestPackageCacheProjectionVisitsOnlyCoreOwners(t *testing.T) {
	source, target, request, route := cacheProjectionPair(t)
	intent := request.Cache[0]
	request.Tools = []Tool{{Kind: FunctionTool, Name: StringValue("lookup"), Cache: []CacheIntent{intent}}}
	payload := Value{raw: `{"cache":[{"kind":"breakpoint","value":{"type":"ephemeral"}}]}`}
	request.Content = []Node{
		{Kind: MessageNode, Role: StringValue("user"), Cache: []CacheIntent{intent}, Children: []Node{{Kind: TextNode, Payload: StringValue("hello"), Cache: []CacheIntent{intent}}}},
		{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("thought"), Cache: []CacheIntent{intent}}}},
		{Kind: ToolCallNode, CallID: StringValue("c"), Name: StringValue("lookup"), Input: &ToolInput{Kind: JSONInput, Value: payload}},
	}
	conversion, err := ResolveConversion(DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	sink := &DiagnosticSink{}
	projected, err := conversion.Request(t.Context(), request, route, sink)
	if err != nil || hasRequestCacheIntents(projected) || !hasRequestCacheIntents(request) || projected.Content[2].Input.Value != payload || len(sink.Issues()) != 5 {
		t.Fatal("projection confused owners with user JSON", err, sink.Issues())
	}
	// No cache means no per-owner output plan, document encoding or effect state.
	empty := &Request{SchemaVersion: ProtocolCoreVersion, Content: make([]Node, 128)}
	rule := conversion.Policy.Rules[0]
	options, _ := readConversionOption[requestProjectionOptions](conversion, rule)
	allocations := testing.AllocsPerRun(20, func() {
		if err := conversion.projectRequestIntents(t.Context(), empty, route, rule, options.corePlan, nil); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatal("empty cache projection constructs temporary state", allocations)
	}
}

func TestPackageCacheProjectionRejectsResourceAndNativeRecovery(t *testing.T) {
	source, _, request, route := cacheProjectionPair(t)
	document := noCachePackage(t)
	entry := document.Mapping.Directions[EncodeRequest]
	entry.Effects[3].When = expressionPointer(literalExpression(true))
	document.Mapping.Directions[EncodeRequest] = entry
	document.Mapping.Expressions = nil
	target := compilePackageForTest(t, document)
	route.Target = target.Identity()
	conversion, err := ResolveConversion(DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	resource := request.Clone()
	resource.Cache = []CacheIntent{{Kind: "resource", Resource: &Resource{Kind: "cache", ID: StringValue("cache/1"), Scope: Scope{Provider: "source", Account: "isolated"}}}}
	_, err = conversion.Request(t.Context(), resource, route, &DiagnosticSink{})
	issue := assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/cache/0")
	if issue.RuleID != "request-projection" || issue.PolicyHash != conversion.Hash {
		t.Fatal("resource rejection lost its policy evidence", issue)
	}
	if resource.Cache[0].Resource.Scope.Account != "isolated" {
		t.Fatal("projection mutated resource ownership")
	}
	unknown := request.Clone()
	unknown.Cache = []CacheIntent{{Kind: "future", Value: Value{raw: "null"}}}
	_, err = conversion.Request(t.Context(), unknown, route, &DiagnosticSink{})
	assertPackageRuntimeIssue(t, err, InvalidInput, "/cache/0/kind")
	// The disposition's resource restriction does not change the existing,
	// separately authored remove operation.
	remove := ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "operator", Rules: []ConversionRule{{ID: "request-projection", Order: 130, Enabled: true, Phase: ConversionRequest, Action: "remove", Path: "/cache"}}}
	explicit, err := ResolveConversion(DefaultConversionPolicy(source, target), remove)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := explicit.Request(t.Context(), resource, route, &DiagnosticSink{})
	if err != nil || len(cleared.Cache) != 0 {
		t.Fatal("disposition changed explicit remove semantics", err)
	}
	document = noCachePackage(t)
	document.Spec.Family = source.Identity().Family
	document.Mapping.Native.Preserve = true
	sameWire := compilePackageForTest(t, document)
	route.Target = sameWire.Identity()
	conversion, _ = ResolveConversion(DefaultConversionPolicy(source, sameWire))
	_, err = conversion.Request(t.Context(), request, route, &DiagnosticSink{})
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/cache/0")
	// Editable policy metadata cannot authorize the target's native replay.
	policy := DefaultConversionPolicy(source, sameWire)
	var options requestProjectionOptions
	if err := policy.Rules[0].Value.Decode(&options); err != nil {
		t.Fatal(err)
	}
	options.Core.Native = false
	policy.Rules[0].Value = fixtureValue(t, options)
	conversion, err = ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := conversion.Request(t.Context(), request, route, &DiagnosticSink{})
	if err != nil || len(projected.Cache) != 0 {
		t.Fatal("synthetic policy did not exercise post-projection validation", err)
	}
	_, err = sameWire.EncodeRequest(t.Context(), projected, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
}

func TestPackageCacheProjectionRejectsUnboundedDeclarations(t *testing.T) {
	for _, change := range []func(*ProtocolSemanticEffect){
		func(e *ProtocolSemanticEffect) { e.Default = StringValue("all") },
		func(e *ProtocolSemanticEffect) { e.Default = Value{} },
		func(e *ProtocolSemanticEffect) { e.When = nil },
		func(e *ProtocolSemanticEffect) { e.Path = "/cache" },
		func(e *ProtocolSemanticEffect) { e.When = expressionPointer(readExpression("context", "/flag")) },
	} {
		document := noCachePackage(t)
		change(&document.Mapping.Directions[EncodeRequest].Effects[2])
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		value := fixtureValue(t, document)
		if compiled, issues := compiler.CompilePackage(value.Bytes()); compiled != nil || IssuesError(issues) == nil {
			t.Fatal("unbounded cache default compiled")
		}
	}
	document := noCachePackage(t)
	document.Mapping.Directions[EncodeRequest].Effects[3].Severity = SeverityWarning
	target := compilePackageForTest(t, document)
	var options requestProjectionOptions
	if err := target.packagePlan.requestProjection.Decode(&options); err != nil {
		t.Fatal(err)
	}
	options.Core.Effects = []ProtocolSemanticEffect{{ID: "wrong-kind", Kind: "emit"}}
	policy := DefaultConversionPolicy(target, target)
	policy.Rules[0].Value = fixtureValue(t, options)
	if _, err := ResolveConversion(policy); err == nil {
		t.Fatal("non-disposition option bypassed compilation")
	}
}
