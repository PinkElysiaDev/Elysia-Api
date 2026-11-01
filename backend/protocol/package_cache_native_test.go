package protocol

import "testing"

func TestPackageCacheNativeAllowsUnchangedBaselines(t *testing.T) {
	for _, hasExplicitLimit := range []bool{false, true} {
		document := noCachePackage(t)
		document.Mapping.Native.Preserve = true
		document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.AllowUnknown = true
		if hasExplicitLimit {
			limits := DefaultLimits()
			limits.BufferBytes = 1 << 20
			document.Mapping.Limits = &limits
		}
		compiled := compilePackageForTest(t, document)
		calls := 0
		options := EvaluationContext{Values: Object{"binding": StringValue("original")}, ResolveRequestScope: func(request *Request) (Scope, error) {
			calls++
			request.Model = StringValue("authorized")
			return Scope{}, nil
		}}
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello","unmapped":9007199254740993}`), options)
		if err != nil {
			t.Fatal(err)
		}
		workspace := request.Native.requestWorkspace
		if workspace == nil || !workspace.baseline.Model.IsString("m") || request.Clone().Native.requestWorkspace != workspace || cloneRequestSemantic(request).Native.requestWorkspace != workspace {
			t.Fatal("private immutable baseline did not survive semantic cloning")
		}
		conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		output, err := compiled.EncodeRequestValue(t.Context(), projected, options)
		if err != nil || output.raw != `{"model":"authorized","prompt":"hello","unmapped":9007199254740993}` || calls != 1 {
			t.Fatal("empty cache replay failed or repeated authorization", output.raw, calls, err)
		}
		if request.Native.requestWorkspace != workspace || !workspace.baseline.Model.IsString("m") {
			t.Fatal("native reconstruction mutated the source workspace")
		}
		report := Verify(t.Context(), compiled)
		if !report.Passed {
			t.Fatal("same-object native offline roundtrip was rejected", report)
		}
	}
}

func TestPackageCacheNativeKeepsOriginalDispositionEvidence(t *testing.T) {
	document := cacheOwnerPackage(t)
	dispositions := noCachePackage(t)
	document.Mapping.Expressions = dispositions.Mapping.Expressions
	document.Mapping.ConversionDefaults = dispositions.Mapping.ConversionDefaults
	entry := document.Mapping.Directions[EncodeRequest]
	entry.Effects = append(entry.Effects, dispositions.Mapping.Directions[EncodeRequest].Effects[2:]...)
	document.Mapping.Directions[EncodeRequest] = entry
	compiled := compilePackageForTest(t, document)
	for _, policy := range []string{`{"type":"ephemeral","future":true}`, `{"type":"ephemeral","ttl":"1h"}`} {
		raw := `{"model":"m","prompt":"hello","cache_control":` + policy + `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(raw), EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if policy == `{"type":"ephemeral","future":true}` {
			output, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
			if err != nil || output.raw != raw {
				t.Fatal("an unmatched original cache intent blocked native replay", output.raw, err)
			}
			continue
		}
		cleared := cloneRequestSemantic(request)
		cleared.Cache = nil
		_, err = compiled.EncodeRequestValue(t.Context(), cleared, EvaluationContext{})
		assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
		// The immutable compiled policy, not this editable rule value, determines
		// whether the original baseline contains a disposition candidate.
		conversionPolicy := DefaultConversionPolicy(compiled, compiled)
		var options requestProjectionOptions
		if err := conversionPolicy.Rules[0].Value.Decode(&options); err != nil {
			t.Fatal(err)
		}
		options.Core.Native = false
		conversionPolicy.Rules[0].Value = fixtureValue(t, options)
		conversion, err := ResolveConversion(conversionPolicy)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
		if err != nil || len(projected.Cache) != 0 || len(request.Cache) != 1 {
			t.Fatal("fixture did not remove cache in the private candidate", err)
		}
		_, err = compiled.EncodeRequestValue(t.Context(), projected, EvaluationContext{})
		assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
	}
}

func TestPackageCacheNativeRequiresExactWorkspace(t *testing.T) {
	document := noCachePackage(t)
	document.Mapping.Native.Preserve = true
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{Values: Object{"binding": StringValue("original")}, Scope: Scope{Provider: "p", Account: "a"}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Request)
	}{
		{"source", func(r *Request) { r.Native.Source.Path = "/changed" }},
		{"value", func(r *Request) { r.Native.Value = Value{raw: `{"model":"changed","prompt":"hello"}`} }},
		{"context", func(r *Request) { r.Native.RequestContext.Values["binding"] = StringValue("changed") }},
		{"context-scope", func(r *Request) { r.Native.RequestContext.Scope.Account = "other" }},
		{"scope", func(r *Request) { r.Native.Source.Scope.Account = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRequestSemantic(request)
			test.change(changed)
			_, err := compiled.EncodeRequestValue(t.Context(), changed, options)
			assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
		})
	}
	independent := compilePackageForTest(t, document)
	_, err = independent.EncodeRequestValue(t.Context(), request, options)
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
	serialized := fixtureValue(t, request)
	withoutWorkspace, err := decodeRequestContract(serialized.Bytes(), ProtocolCoreVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiled.EncodeRequestValue(t.Context(), withoutWorkspace, options)
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
	tighter, err := WithRequestBodyLimit(t.Context(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiled.EncodeRequestValue(tighter, request, options)
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
	options.Scope.Account = "other"
	_, err = compiled.EncodeRequestValue(t.Context(), request, options)
	if !hasIssueCode(err, ResourceScopeMismatch) {
		t.Fatal("allowing the baseline bypassed final native scope checks", err)
	}
}

func TestPackageCacheNativeMatchesBoundResourceScope(t *testing.T) {
	document := noCachePackage(t)
	document.Mapping.Native.Preserve = true
	document.Mapping.Capabilities[CacheResourcesCapability] = true
	document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.Properties["cache_ref"] = ValueSchema{Type: StringType}
	decode := document.Mapping.Directions[DecodeRequest]
	decode.Program.Fields["cache"] = Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
		"kind": literalExpression("resource"), "resource": objectExpression(map[string]Expression{"kind": literalExpression("cache"), "id": readExpression("input", "/cache_ref")}),
	})}}
	decode.Effects = append(decode.Effects, ProtocolSemanticEffect{ID: "cache-resource", Kind: "emit", Path: "/cache/0/resource/id", Target: "/cache_ref"})
	document.Mapping.Directions[DecodeRequest] = decode
	encode := document.Mapping.Directions[EncodeRequest]
	encode.Effects = append(encode.Effects, ProtocolSemanticEffect{ID: "scoped-cache", Kind: "drop", Scope: cacheIntentScope, Severity: SeverityWarning, Reason: "synthetic scoped resource rejection", When: &Expression{Op: "equal", Items: []Expression{readExpression("input", "/resource/scope/provider"), literalExpression("p")}}})
	document.Mapping.Directions[EncodeRequest] = encode
	compiled := compilePackageForTest(t, document)
	scope := Scope{Provider: "p", Account: "a"}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello","cache_ref":"cache/1"}`), EvaluationContext{ResolveRequestScope: func(*Request) (Scope, error) { return scope, nil }})
	if err != nil {
		t.Fatal(err)
	}
	baseline := request.Native.requestWorkspace.baseline
	if baseline.Cache[0].Resource.Scope != (Scope{}) || request.Cache[0].Resource.Scope != scope {
		t.Fatal("fixture did not separate decoder and bound resource scopes")
	}
	request.Cache = nil
	_, err = compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{Scope: scope})
	assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
	if baseline.Cache[0].Resource.Scope != (Scope{}) {
		t.Fatal("baseline cache matching mutated its immutable resource")
	}
}
