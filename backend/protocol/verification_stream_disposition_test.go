package protocol

import "testing"

func streamCompanionPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := dispositionPackage(t)
	document.Mapping.ConversionDefaults[0].StreamDisposition.ResponseRule = "collected-layout"
	document.Mapping.ConversionDefaults = append(document.Mapping.ConversionDefaults, ProtocolConversionDefault{
		ID: "collected-layout", Order: 230, Enabled: true, Phase: ConversionResponse,
		Steps: []ProtocolControlStep{{Set: map[string]Expression{"/model": literalExpression("projected")}}},
	})
	return document
}

func TestStreamVerificationRequiresCommittedCompanion(t *testing.T) {
	compiled := compilePackageForTest(t, streamCompanionPackage(t))
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.identity, Target: compiled.identity, Transport: SSE}
	options := (EvaluationContext{State: NewEvaluationState(), Diagnostics: &DiagnosticSink{}}).WithConversion(conversion, route)
	defer options.State.Release()
	event := Event{SchemaVersion: compiled.CoreVersion(), Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("i"), Delta: StringValue("x")}
	response := &Response{SchemaVersion: compiled.CoreVersion(), Model: StringValue("source")}
	if _, err := compiled.EncodeFrames(t.Context(), event, options); err == nil {
		t.Fatal("invalid output was accepted")
	}
	if err := projectStreamVerification(t.Context(), compiled, conversion, route, options.Diagnostics.Issues(), response); err != nil || !response.Model.IsString("source") {
		t.Fatal("failed transaction authorized comparison projection", response, err)
	}
	event.Sequence = coreInteger(0)
	if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
		t.Fatal(err)
	}
	if err := projectStreamVerification(t.Context(), compiled, conversion, route, options.Diagnostics.Issues(), response); err != nil || !response.Model.IsString("projected") {
		t.Fatal("committed companion was not applied", response, err)
	}
	actual := &Response{SchemaVersion: compiled.CoreVersion(), Model: StringValue("wrong")}
	if err := compareRoundTrip(compiled, Sample{Direction: DecodeEvent}, response, actual); err == nil {
		t.Fatal("companion projection hid an incorrect actual result")
	}
}

func TestStreamVerificationCompanionRemainsPinned(t *testing.T) {
	compiled := compilePackageForTest(t, streamCompanionPackage(t))
	for _, name := range []string{"disabled", "replaced", "unmatched", "removed"} {
		t.Run(name, func(t *testing.T) {
			policy := DefaultConversionPolicy(compiled, compiled)
			for index := range policy.Rules {
				if policy.Rules[index].ID != "collected-layout" {
					continue
				}
				switch name {
				case "disabled":
					policy.Rules[index].Enabled = false
				case "replaced":
					policy.Rules[index].Action, policy.Rules[index].Value = "warn", Value{}
				case "unmatched":
					policy.Rules[index].Match.Model = "other"
				case "removed":
					policy.Rules = append(policy.Rules[:index], policy.Rules[index+1:]...)
				}
				break
			}
			conversion, err := ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := ConversionContext{Source: compiled.identity, Target: compiled.identity, Transport: SSE}
			options := (EvaluationContext{State: NewEvaluationState(), Diagnostics: &DiagnosticSink{}}).WithConversion(conversion, route)
			defer options.State.Release()
			event := Event{SchemaVersion: compiled.CoreVersion(), Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("i"), Sequence: coreInteger(0), Delta: StringValue("x")}
			if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
				t.Fatal(err)
			}
			response := &Response{SchemaVersion: compiled.CoreVersion()}
			if err := projectStreamVerification(t.Context(), compiled, conversion, route, options.Diagnostics.Issues(), response); err == nil {
				t.Fatal("unavailable companion authorized comparison projection")
			}
		})
	}
}

func TestStreamCompanionCompileBoundary(t *testing.T) {
	for _, name := range []string{"missing", "event", "non-route", "reject"} {
		t.Run(name, func(t *testing.T) {
			document := streamCompanionPackage(t)
			switch name {
			case "missing":
				document.Mapping.ConversionDefaults[0].StreamDisposition.ResponseRule = "absent"
			case "event":
				document.Mapping.ConversionDefaults[0].StreamDisposition.ResponseRule = "layout"
			case "non-route":
				yes := true
				document.Mapping.ConversionDefaults[1].Match.Present = &yes
			case "reject":
				document.Mapping.ConversionDefaults[0].StreamDisposition.Kind = "reject"
			}
			compiler, _ := NewCompiler(DefaultLimits(), nil)
			if compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("invalid companion compiled", issues)
			}
		})
	}
}
