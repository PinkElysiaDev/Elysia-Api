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
