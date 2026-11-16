package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesCanonicalReferenceProof(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	metadata := p.ResponseMetadata{
		Codec: "custom", SourceCodec: "custom", Name: "provider_links", Location: "content", Path: "/provider_links",
		Meaning: "url-references", References: testValue(t, `[{"url":"https://example.test","title":"reference","start":0,"end":2}]`),
		Value: testValue(t, `{"opaque":"unrelated wire representation","start_index":100}`),
	}
	before := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Content: []p.Node{{
		Kind: p.TextNode, ID: p.StringValue("text"), Payload: p.StringValue("ab"), Metadata: []p.ResponseMetadata{metadata},
	}}}
	for _, text := range []string{"ab", "xy"} {
		conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "canonical-references", Rules: []p.ConversionRule{{
			ID: "edit", Enabled: true, Phase: p.ConversionResponse, Match: p.ConversionMatch{NodeKind: p.TextNode}, Action: "set", Path: "/payload", Value: p.StringValue(text),
		}}})
		if err != nil {
			t.Fatal(err)
		}
		after, err := conversion.Response(t.Context(), before, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = compiled.ValidateResponseReferences(t.Context(), after)
		if (err == nil) != (text == "ab") || err != nil && !strings.Contains(err.Error(), "/provider_links") {
			t.Fatal("canonical reference proof depended on provider wire data", text, err)
		}
	}
}

func TestResponsesBufferingEventRebuild(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, eventType := range []string{"response.created", "response.in_progress", "response.output_text.delta", "response.completed"} {
		t.Run(eventType, func(t *testing.T) {
			source := responsesPaddedTextFrames()
			if eventType == "response.in_progress" {
				source = append(source[:1], append([]string{`{"type":"response.in_progress","response":{"id":"r"}}`}, source[1:]...)...)
			}
			for i, raw := range source {
				fields, _ := testValue(t, raw).ReadObject()
				if fields["type"].JSONString() == p.StringValue(eventType).JSONString() {
					fields["safety_buffering"] = testValue(t, `false`)
					wire, err := p.EncodeValue(fields)
					if err != nil {
						t.Fatal(err)
					}
					source[i] = wire.JSONString()
				}
			}
			frames, _ := decodePackageStream(t, compiled, source, p.Scope{})
			output := encodePackageStream(t, compiled, frames, p.Scope{}, false)
			found := false
			for _, raw := range output {
				fields, _ := testValue(t, raw).ReadObject()
				if !fields["safety_buffering"].IsZero() {
					found = fields["type"].JSONString() == p.StringValue(eventType).JSONString() && fields["safety_buffering"].JSONString() == "false"
					if !found {
						t.Fatal("buffering metadata changed owner", raw)
					}
				}
			}
			if !found {
				t.Fatal("buffering metadata disappeared", output)
			}
		})
	}
}

func TestResponsesUnknownUsageStaysOpaque(t *testing.T) {
	from, to := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct{ name, field, value string }{
		{"attribution-root", "attribution", `{"vendor":true}`},
		{"attribution-bucket", "attribution", `{"items":{"message":{"input_tokens":1,"vendor":true}}}`},
		{"attribution-part", "attribution", `{"items":{"message":{"content":[{"input_tokens":1,"vendor":true}]}}}`},
		{"billing-policy", "linapi_cache_billing", `{"basis":"vendor-policy"}`},
		{"billing-field", "linapi_cache_billing", `{"basis":"disclosed_billing_allocation","vendor":true}`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			response, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","output":[],"usage":{"`+sample.field+`":`+sample.value+`}}`), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Usage != nil && (!response.Usage.Attribution.IsZero() || !response.Usage.CacheBilling.IsZero()) {
				t.Fatal("unknown accounting acquired canonical semantics", response.Usage)
			}
			response.Native = nil
			wire, err := from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil || !strings.Contains(string(wire), sample.value) {
				t.Fatal("own opaque accounting changed", string(wire), err)
			}
			projected, err := conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, response.Source), nil)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			}
			if err == nil {
				t.Fatal("opaque accounting disappeared across protocols")
			}
		})
	}
}

func TestResponsesRequestProjectionUsesCanonicalSafety(t *testing.T) {
	target := compileBuiltinPackage(t, "openai-responses")
	source := p.Identity{Family: "independent-safety-source", WireVersion: "1"}
	for _, raw := range []string{`null`, `[]`, `[{"category":"harassment","threshold":"none"}]`, `[{"category":"harassment","threshold":"low"}]`, `[{"category":"harassment","threshold":"none","unknown":true}]`} {
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(mode+"/"+raw, func(t *testing.T) {
				policy := declaredRulePolicy(t, target, target, "safety-settings", "compatible")
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for i := range policy.Rules {
						if policy.Rules[i].ID == "safety-settings" {
							policy.Rules[i].Enabled = false
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Source: source, Model: p.StringValue("m"), Content: []p.Node{}, ClientOutput: &p.ClientOutput{ResponsesStorage: &p.ResponsesStorageIntent{}}, Parameters: p.Object{
					"user": p.StringValue("core-identity"), "safety": testValue(t, raw),
				}}
				before, _ := p.EncodeValue(request)
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source, Target: target.Identity()}, sink)
				var wire []byte
				if err == nil {
					wire, err = target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
				}
				isEmpty := raw == "null" || raw == "[]"
				canEncode := mode != "disabled" && (isEmpty || mode == "compatible" && !strings.Contains(raw, "low") && !strings.Contains(raw, "unknown"))
				if (err == nil) != canEncode {
					t.Fatal("canonical safety handling depended on a provider wire shape", string(wire), err)
				}
				if canEncode {
					fields, parseErr := testValue(t, string(wire)).ReadObject()
					if parseErr != nil || !fields["user"].IsString("core-identity") || !fields["safety"].IsZero() || !sinkHas(sink, "/parameters/safety") {
						t.Fatal("canonical projection lost identity or its safety diagnostic", string(wire), sink.Issues(), parseErr)
					}
				}
				after, _ := p.EncodeValue(request)
				if before != after {
					t.Fatal("target projection changed its canonical source")
				}
			})
		}
	}
}

func TestResponsesUsageAliasesRequireOwnNative(t *testing.T) {
	target := compileBuiltinPackage(t, "openai-responses")
	document, _ := target.Package()
	document.Spec.ID, document.Spec.Family = "foreign-usage-aliases", "foreign-usage"
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := p.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	foreign, issues := compiler.CompilePackage(definition.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	const usage = `{"cache_creation_input_tokens":30,"input_tokens_details":{"cached_creation_tokens":30}}`
	for _, source := range []*p.Compiled{target, foreign} {
		t.Run(source.Identity().Family, func(t *testing.T) {
			response, err := source.DecodeResponse(t.Context(), []byte(cacheAliasResponse(Responses, usage)), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			response.Usage.CacheCreation.Count = 31
			wire, err := target.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			wantAlias := source == target
			for _, field := range []string{"cache_creation_input_tokens", "cached_creation_tokens"} {
				if strings.Contains(string(wire), `"`+field+`":31`) != wantAlias {
					t.Fatal("usage alias presence escaped native ownership", string(wire))
				}
			}
			if !strings.Contains(string(wire), `"cache_write_tokens":31`) || strings.Contains(string(wire), ":30") {
				t.Fatal("native alias overrode the canonical count", string(wire))
			}
		})
	}
}
