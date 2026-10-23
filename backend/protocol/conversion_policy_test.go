package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestConversionOrderOverridesAndProvenance(t *testing.T) {
	base := ConversionPolicy{SchemaVersion: 1, ID: "base", Mode: "compatible", Rules: []ConversionRule{
		{ID: "first", Order: 100, Enabled: true, Phase: ConversionRequest, Action: "set", Path: "/parameters/temperature", Value: StringValue("mapped")},
		{ID: "second", Order: 200, Enabled: true, Phase: ConversionRequest, Action: "reject", Match: ConversionMatch{Path: "/parameters/temperature", Present: boolPtr(true)}, Reason: "order observed"},
	}}
	c, err := ResolveConversion(base)
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: SemanticSchemaVersion, Parameters: Object{"stream": StringValue("false")}}
	if _, err = c.Request(t.Context(), request, ConversionContext{}, nil); err == nil || !strings.Contains(err.Error(), "order observed") {
		t.Fatal(err)
	}
	override := ConversionPolicy{SchemaVersion: 1, ID: "model", Rules: []ConversionRule{{ID: "second", Order: 200, Enabled: false, Phase: ConversionRequest, Action: "reject"}}}
	c, err = ResolveConversion(base, override)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Request(t.Context(), request, ConversionContext{}, nil)
	if err != nil || out.Parameters["temperature"] != StringValue("mapped") || len(request.Parameters) != 1 || c.Origins["second"] != "model" {
		t.Fatal(out, err)
	}
	base.Rules[1].Order = 100
	if _, err = CompileConversion(base); err == nil {
		t.Fatal("ambiguous ordering accepted")
	}
	original, _ := EncodeValue(Node{Kind: TextNode, Payload: StringValue("signed"), Resources: []Resource{{Kind: "signature", ID: StringValue("signature")}}})
	changed, _ := EncodeValue(Node{Kind: TextNode, Payload: StringValue("tampered"), Resources: []Resource{{Kind: "signature", ID: StringValue("signature")}}})
	if err = protectConversionProvenance(original, changed); err == nil {
		t.Fatal("signature reassociated with modified content")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestConversionValueChangeUsesWireBytes(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		hasChange           bool
	}{
		{"same", `{"value":1}`, `{"value":1}`, false},
		{"number spelling", `1`, `1.0`, true},
		{"whitespace", `{ "value":1 }`, `{"value":1}`, true},
		{"key order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"escaped string", `"a"`, `"\u0061"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := parseTestValue(t, test.before)
			after := parseTestValue(t, strings.Clone(test.after))
			policy := ConversionPolicy{SchemaVersion: 1, ID: "wire-change", Mode: "compatible", Rules: []ConversionRule{{
				ID: "replace", Order: 1, Enabled: true, Phase: ConversionWire, Action: "transform",
				Expression: &Expression{Op: "literal", Value: after},
			}}}
			compiled, err := CompileConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &DiagnosticSink{}
			output, err := compiled.ApplyValue(t.Context(), ConversionWire, before, ConversionContext{}, sink)
			if err != nil || output != after {
				t.Fatalf("output %q, error %v", output.raw, err)
			}
			if hasIssue := len(sink.Issues()) != 0; hasIssue != test.hasChange {
				t.Fatalf("byte-change diagnostic differs: %+v", sink.Issues())
			}
		})
	}
}

func TestUnchangedConversionProvenanceDoesNotValidateScope(t *testing.T) {
	values := []Value{{}}
	for _, raw := range []string{`null`, `false`, `42`, `"text"`, `[]`, `{}`, `[{"scope":null}]`, `{"scope":{"account":"other"}}`, `{"resources":{"scope":false}}`} {
		values = append(values, parseTestValue(t, raw))
	}
	for _, before := range values {
		after := before
		if !before.IsZero() {
			after = parseTestValue(t, strings.Clone(before.raw))
		}
		if err := protectConversionProvenance(before, after); err != nil {
			t.Fatalf("unchanged %q rejected: %v", before.raw, err)
		}
	}
	before := parseTestValue(t, `{"scope":{"account":"original"}}`)
	after := parseTestValue(t, `{"scope":{"account":"other"}}`)
	if err := protectConversionProvenance(before, after); err == nil {
		t.Fatal("changed protected scope was accepted")
	}
}

func TestConversionNodeRulesAndLimits(t *testing.T) {
	policy := ConversionPolicy{SchemaVersion: 1, ID: "node-policy", Rules: []ConversionRule{{ID: "text", Order: 1, Enabled: true, Phase: ConversionResponse, Match: ConversionMatch{NodeKind: TextNode}, Action: "set", Path: "/payload", Value: StringValue("changed")}}}
	c, err := CompileConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	original := &Response{Content: []Node{{Kind: MessageNode, Children: []Node{{Kind: TextNode, Payload: StringValue("original")}, {Kind: ToolCallNode, Name: StringValue("untouched")}}}}}
	out, err := c.Response(t.Context(), original, ConversionContext{}, nil)
	if err != nil || out.Content[0].Children[0].Payload != StringValue("changed") || original.Content[0].Children[0].Payload != StringValue("original") || out.Content[0].Children[1].Name != StringValue("untouched") {
		t.Fatal(out, err)
	}
	limit := DefaultLimits()
	limit.BufferBytes++
	policy.Limits = &limit
	if _, err = CompileConversion(policy); err == nil {
		t.Fatal("policy raised engine limit")
	}
}

func TestContinuationAuthenticationAndOwnership(t *testing.T) {
	codec, err := NewContinuationCodec([]byte("test-master"))
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{Provider: "source", Account: "account", Model: "actual-id"}
	identity := Identity{Family: "gemini", WireVersion: "v2"}
	node := Node{Kind: TextNode, Payload: StringValue("original"), Resources: []Resource{{Kind: "signature", ID: StringValue("signature")}}}
	record := ContinuationRecord{Version: 1, Owner: "owner", Scope: scope, Protocol: identity, Node: node, Digest: ContinuationNodeDigest(node), ExpiresAt: time.Now().Unix() + 60}
	token, err := codec.Seal(record, 4096)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := codec.Open(token, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target := Node{Kind: TextNode, Payload: StringValue("original")}
	if err = RestoreContinuation(&target, opened, "owner", scope, identity); err != nil || !HasSignature(target) {
		t.Fatal(err, target)
	}
	if err = RestoreContinuation(&target, opened, "other-owner", scope, identity); err == nil {
		t.Fatal("cross-owner restoration")
	}
	scope.Model = "different-model"
	if err = RestoreContinuation(&target, opened, "owner", scope, identity); err == nil {
		t.Fatal("cross-model restoration")
	}
	mutated := token[:len(token)-8] + "AAAAAAAA"
	if _, err = codec.Open(mutated, 4096); err == nil {
		t.Fatal("tampered token accepted")
	}
	record.ExpiresAt = time.Now().Unix() - 1
	token, _ = codec.Seal(record, 4096)
	if _, err = codec.Open(token, 4096); err == nil {
		t.Fatal("expired token accepted")
	}
	other, _ := NewContinuationCodec([]byte("another-master"))
	if _, err = other.Open(token, 4096); err == nil {
		t.Fatal("wrong key accepted")
	}
}
