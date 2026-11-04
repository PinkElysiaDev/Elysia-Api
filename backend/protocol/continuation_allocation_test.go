package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Keep the original digest boundary as a compatibility oracle. A faster digest
// must authenticate exactly the same bytes as already-issued carriers.
func originalContinuationDigest(node Node) string {
	node = CanonicalReasoning(node)
	node.ID, node.Status, node.Role = Value{}, Value{}, Value{}
	node.Source, node.Native = nil, nil
	node.Resources, node.Cache, node.Attributes = nil, nil, nil
	if len(node.Children) > 0 {
		children := cloneNodes(node.Children)
		var clean func([]Node)
		clean = func(nodes []Node) {
			for i := range nodes {
				n := &nodes[i]
				n.ID, n.Status, n.Role = Value{}, Value{}, Value{}
				n.Source, n.Native, n.Resources, n.Cache, n.Attributes = nil, nil, nil, nil, nil
				clean(n.Children)
			}
		}
		clean(children)
		node.Children = children
	}
	if node.ReasoningContent != nil {
		parts := comparableNodes(node.ReasoningContent)
		if parts == nil {
			parts = []Node{}
		}
		node.ReasoningContent = parts
	}
	value, _ := EncodeValue(node)
	return hashValue(value)
}

func TestContinuationDigestEncodingCompatibility(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `9007199254740993`, `1e100`, ` {"a":1,"a":2,"\u0061":3} `, `"<>&\u2028\u2029"`, `"\ud800"`, "\"\xff\"", `[]`, `{}`, `"` + strings.Repeat("A", 4096) + `"`, ` {"text":"` + strings.Repeat("x<>&😀\u2028", 1024) + `","n":9007199254740993,"n":1e100} `} {
		payload := parseTestValue(t, raw)
		for _, node := range []Node{
			{Kind: TextNode, Payload: payload},
			{Kind: ToolCallNode, CallID: StringValue("call"), Name: StringValue("tool"), Input: &ToolInput{Kind: JSONInput, Value: payload}},
			{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: payload}}},
			{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: TextNode, Payload: payload, ID: StringValue("ignored"), Children: []Node{{Kind: TextNode, Payload: payload}}}}},
		} {
			before, err := EncodeValue(node)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := ContinuationNodeDigest(node), originalContinuationDigest(node); got != want {
				t.Fatalf("digest changed for %s: %s != %s", before.JSONString(), got, want)
			}
			after, err := EncodeValue(node)
			if err != nil || before != after {
				t.Fatal("digest mutated input", err)
			}
		}
	}
}

func BenchmarkContinuationNodeDigest(b *testing.B) {
	for _, size := range []int{32, 1024, 2048, 4096, 2 << 20} {
		node := Node{Kind: TextNode, Payload: StringValue(strings.Repeat("A", size))}
		want := originalContinuationDigest(node)
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if ContinuationNodeDigest(node) != want {
					b.Fatal("digest changed")
				}
			}
		})
	}
}

func BenchmarkContinuationMediaDigest(b *testing.B) {
	payload := Value{raw: `{"mimeType":"image/png","data":"` + strings.Repeat("A", 2<<20) + `"}`}
	node := Node{Kind: ToolResultNode, CallID: StringValue("call"), Children: []Node{{Kind: ImageNode, Payload: payload}}}
	want := originalContinuationDigest(node)
	b.ReportAllocs()
	for b.Loop() {
		if ContinuationNodeDigest(node) != want {
			b.Fatal("media digest changed")
		}
	}
}

func BenchmarkSemanticOwnership(b *testing.B) {
	for _, count := range []int{1, 32, 128} {
		request := &Request{Model: StringValue("m"), Parameters: Object{"n": {raw: `9007199254740993`}}}
		for range count {
			request.Content = append(request.Content, Node{Kind: TextNode, Payload: StringValue("plain😀"), Attributes: Object{"x": StringValue("value")}})
		}
		b.Run(fmt.Sprintf("clone-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy := cloneRequestSemantic(request)
				if len(copy.Content) != count {
					b.Fatal("clone lost content")
				}
			}
		})
		b.Run(fmt.Sprintf("normalize-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy := normalizeMappingSemantic(request, DefaultLimits())
				if len(copy.Content) != count {
					b.Fatal("normalization lost content")
				}
			}
		})
	}
}

func TestSemanticOwnershipCopiesNativeContainers(t *testing.T) {
	request := &Request{Content: []Node{{Kind: TextNode, Payload: StringValue("unchanged"), Native: &Native{Value: StringValue("wire")}, Attributes: Object{"x": StringValue("original")}}}}
	copy := cloneRequestSemantic(request)
	copy.Content[0].Native.Value = StringValue("changed")
	copy.Content[0].Attributes["x"] = StringValue("changed")
	if !request.Content[0].Native.Value.IsString("wire") || !request.Content[0].Attributes["x"].IsString("original") || !reflect.DeepEqual(copy.Content[0].Payload, request.Content[0].Payload) {
		t.Fatal("semantic clone changed ownership")
	}
}
