package protocol

import (
	"strings"
	"testing"
	"time"
)

func continuationTestPackage(t *testing.T) ProtocolPackage {
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	document.Spec.Documents["request"].Schema.Properties["carrier"] = ValueSchema{Type: StringType}
	document.Spec.Documents["client-response"].Schema.Properties["carrier"] = ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: StringType}}
	selection := objectExpression(map[string]Expression{
		"token":  readExpression("input", "/carrier"),
		"source": {Op: "capture_source", Source: expressionPointer(readExpression("input", "/carrier"))},
		"remove": {Op: "capture_source", Source: expressionPointer(readExpression("input", "/carrier"))},
	})
	extract := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/carrier"))}), Then: expressionPointer(Expression{Op: "array", Items: []Expression{selection}}), Otherwise: expressionPointer(literalExpression([]string{}))}
	tokens := Expression{Op: "map", Source: expressionPointer(readExpression("runtime", "/continuation/tokens")), Body: expressionPointer(readExpression("item", "/token"))}
	document.Mapping.Continuation = &ProtocolContinuationMapping{Extract: &extract, EncodeResponse: &ProtocolContinuationOutput{
		Program: objectExpression(map[string]Expression{"reply": readExpression("input", "/reply"), "carrier": tokens}),
		Emitted: readExpression("input", "/carrier"),
	}}
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "response-signatures", Enabled: true, Order: 200, Phase: ConversionResponse, Recovery: &ProtocolRecoveryDisposition{Kinds: []string{"signature"}, OnUnavailable: "degrade"}}}
	return document
}

func TestPackageContinuationAuthenticationBoundary(t *testing.T) {
	compiled := compilePackageForTest(t, continuationTestPackage(t))
	wire := fixtureValue(t, map[string]string{"model": "m", "prompt": "hi", "carrier": ContinuationPrefix + "fixture"})
	options, tokens, err := compiled.ExtractContinuations(t.Context(), wire, EvaluationContext{})
	if err != nil || len(tokens) != 1 {
		t.Fatal(tokens, err)
	}
	request, err := compiled.DecodeRequestValue(t.Context(), wire, options)
	if err != nil {
		t.Fatal(err)
	}
	if request.Native.Value != wire {
		t.Fatal("extraction changed the original native evidence")
	}
	clone := request.Clone()
	clone.Native = nil
	if _, err := compiled.EncodeRequestValue(t.Context(), clone, options); err == nil {
		t.Fatal("removing Native bypassed authentication")
	}
	conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "json-boundary", Rules: []ConversionRule{{ID: "identity", Enabled: true, Order: 1, Phase: ConversionIngress, Action: "transform", Expression: expressionPointer(readExpression("input", ""))}}})
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	if _, err := conversion.Request(t.Context(), request, route, nil); err == nil {
		t.Fatal("JSON rules ran before carrier authentication")
	}
	if err := request.AcceptContinuations([]string{"different"}); err == nil {
		t.Fatal("accepted a different authentication set")
	}
	if err := request.AcceptContinuations(tokens); err != nil {
		t.Fatal(err)
	}
	encoded, err := compiled.EncodeRequestValue(t.Context(), request, options)
	if err != nil || strings.Contains(encoded.JSONString(), "carrier") {
		t.Fatal("consumed carrier leaked into native replay", encoded, err)
	}
	if clone.continuationsAccepted {
		t.Fatal("authentication changed another candidate")
	}
	projected, err := conversion.Request(t.Context(), request, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := *projected.Native
	changed.Value = fixtureValue(t, map[string]string{"model": "m", "prompt": "replaced"})
	projected.Native = &changed
	if _, err := compiled.EncodeRequestValue(t.Context(), projected, options); err == nil {
		t.Fatal("replaced Native retained the old authentication receipt")
	}
	projected.Native = nil
	if _, err := compiled.EncodeRequestValue(t.Context(), projected, options); err != nil {
		t.Fatal("removing Native prevented authenticated reconstruction", err)
	}
}

func TestPackageContinuationDeliveryProof(t *testing.T) {
	document := continuationTestPackage(t)
	compiled := compilePackageForTest(t, document)
	source := Identity{DefinitionID: "source", Family: "source", WireVersion: "1"}
	scope := Scope{Provider: "test", Model: "m"}
	node := Node{Kind: TextNode, Payload: StringValue("hi"), Resources: []Resource{{Kind: "signature", ID: StringValue("signed"), Scope: scope}}}
	codec, err := NewContinuationCodec([]byte("fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := codec.SealState(ContinuationRecord{Version: 1, ID: "one", Protocol: source, Scope: scope, Digest: ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	sealed.MarkPersisted()
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: source, Target: compiled.Identity(), Scope: scope}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}
	for _, client := range []bool{false, true} {
		binding, err := compiled.BindContinuation(EncodeResponse, source, scope, client)
		if err != nil {
			t.Fatal(err)
		}
		if err := binding.Append(sealed); err != nil {
			t.Fatal(err)
		}
		_, err = conversion.Response(t.Context(), response, route.WithContinuation(binding, true), nil)
		if !client {
			if err == nil {
				t.Fatal("persistence masqueraded as client delivery")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		options := (EvaluationContext{Scope: scope}).WithContinuation(binding)
		wire, err := compiled.EncodeContinuationResponse(t.Context(), fixtureValue(t, map[string]string{"reply": "hi"}), options)
		if err != nil {
			t.Fatal(err)
		}
		if err := binding.CommitDelivery(); err == nil {
			t.Fatal("rendered output bypassed final observation")
		}
		if err := compiled.ValidateContinuationResponse(t.Context(), wire, options); err != nil {
			t.Fatal(err)
		}
		if !binding.HasPending() {
			t.Fatal("validation counted as delivery")
		}
		if err := binding.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
		if err := binding.Finish(); err != nil {
			t.Fatal(err)
		}
		unchanged, err := compiled.EncodeContinuationResponse(t.Context(), wire, options)
		if err != nil || unchanged != wire {
			t.Fatal("committed carrier was rendered twice", err)
		}
	}
}
