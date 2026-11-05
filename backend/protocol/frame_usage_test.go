package protocol

import (
	"strings"
	"testing"
)

func TestDeclaredFrameUsageObservation(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "frame-counter", Order: 310, Enabled: true, Phase: ConversionEvent, FrameUsage: true, Steps: []ProtocolControlStep{{Set: map[string]Expression{"/usage": readExpression("runtime", "/frame/usage")}}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	start := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r")}
	usage := &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}, Attribution: fixtureValue(t, map[string]any{"private": "not retained"})}
	bound, err := conversion.WithFrameUsage(route, []Event{start, {SchemaVersion: ProtocolCoreVersion, Type: UsageUpdated, Usage: usage}})
	if err != nil {
		t.Fatal(err)
	}
	usage.Input.Count = 19
	result, err := conversion.Event(t.Context(), start, bound, nil)
	if err != nil || result.Usage == nil || result.Usage.Input.Count != 7 || !result.Usage.Attribution.IsZero() {
		t.Fatal("frame observation aliases source counters or retains private objects", result, err)
	}
	if start.Usage != nil || usage.Input.Count != 19 {
		t.Fatal("frame projection changed original accounting")
	}
	for _, updates := range [][]Event{
		{{Usage: &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}}}, {ResponseID: start.ResponseID, Usage: &Usage{Input: &Counter{Count: 9, Origin: ObservedCount}}}},
		{{ResponseID: start.ResponseID, Usage: &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}}}, {Usage: &Usage{Input: &Counter{Count: 9, Origin: ObservedCount}}}},
	} {
		bound, err = conversion.WithFrameUsage(route, updates)
		if err != nil {
			t.Fatal(err)
		}
		result, err := conversion.Event(t.Context(), start, bound, nil)
		if err != nil || result.Usage.Input.Count != 9 {
			t.Fatal("assigning one response reordered counter observations", result, err)
		}
	}
	if _, err := conversion.Event(t.Context(), start, route, nil); err == nil {
		t.Fatal("unbound frame was treated as an empty source frame")
	}
	bound, err = conversion.WithFrameUsage(route, []Event{start})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := conversion.Event(t.Context(), start, bound, nil); err != nil || result.Usage != nil {
		t.Fatal("bound frame invented a zero observation", result, err)
	}
	bound.Scope.Model = "other"
	if _, err := conversion.Event(t.Context(), start, bound, nil); err == nil {
		t.Fatal("frame observation crossed its route scope")
	}
	limited := *conversion
	limited.limits.Nodes, limited.limits.BufferBytes = 4, 64
	for _, usage := range []*Usage{
		{Details: map[string]Counter{"a": {}, "b": {}, "c": {}, "d": {}}},
		{Details: map[string]Counter{strings.Repeat("large-key", 8): {Count: 1, Origin: ObservedCount}}},
	} {
		if _, err := limited.WithFrameUsage(route, []Event{{Usage: usage}}); err == nil {
			t.Fatal("numeric snapshot copied details beyond its configured budget")
		}
	}
	document.Mapping.ConversionDefaults[0].FrameUsage = false
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); IssuesError(issues) == nil {
		t.Fatal("runtime frame read did not require an explicit observation declaration", issues)
	}
}

func TestFrameUsageKeepsSourceIdentityAfterProjection(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{
		{ID: "target-identity", Order: 100, Enabled: true, Phase: ConversionEvent, Steps: []ProtocolControlStep{{Set: map[string]Expression{"/responseId": literalExpression("target")}}}},
		{ID: "frame-counter", Order: 310, Enabled: true, Phase: ConversionEvent, FrameUsage: true, Steps: []ProtocolControlStep{{Set: map[string]Expression{"/usage": readExpression("runtime", "/frame/usage")}}}},
	}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	count := func(n int64) *Usage { return &Usage{Input: &Counter{Count: n, Origin: ObservedCount}} }
	for _, fixture := range []struct {
		name   string
		id     Value
		events []Event
		want   int64
	}{
		{"unassigned", Value{}, []Event{{Usage: count(3)}}, 3},
		{"renamed", StringValue("source"), []Event{{ResponseID: StringValue("source"), Usage: count(5)}}, 5},
		{"multiple", StringValue("source"), []Event{{ResponseID: StringValue("source"), Usage: count(7)}, {ResponseID: StringValue("target"), Usage: count(19)}, {Usage: count(99)}}, 7},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			route, err := conversion.WithFrameUsage(ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, fixture.events)
			if err != nil {
				t.Fatal(err)
			}
			event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: fixture.id}
			result, err := conversion.Event(t.Context(), event, route, nil)
			if err != nil || result.ResponseID != StringValue("target") || result.Usage == nil || result.Usage.Input.Count != fixture.want {
				t.Fatal("target identity changed source usage attribution", result, err)
			}
		})
	}
}
