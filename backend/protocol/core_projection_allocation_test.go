package protocol

import "testing"

func projectionNodes() map[string]Node {
	text := StringValue("plain😀")
	child := Node{Kind: TextNode, Payload: text}
	return map[string]Node{
		"sparse": child,
		"dense": {
			Kind: MessageNode, Metadata: []ResponseMetadata{{Value: text}}, ReasoningForm: StructuredReasoning,
			ReasoningContent: []Node{child}, Role: text, ID: text, CallID: text, Name: text, Status: text, Payload: text,
			Input: &ToolInput{Kind: JSONInput, Value: text}, Children: []Node{child},
			Cache: []CacheIntent{{Kind: "breakpoint", Value: text}}, Resources: []Resource{{Kind: "file", ID: text}},
			Attributes: Object{"label": text}, Source: &Provenance{}, Native: &Native{Value: text},
		},
	}
}

func TestCoreProjectionPresenceParity(t *testing.T) {
	for name, node := range projectionNodes() {
		t.Run(name, func(t *testing.T) {
			assertCoreReaderParity(t, node, func(pointer []string) (JSONOutput, error) {
				return readCoreNode(node, pointer, nil)
			})
		})
	}
}

func BenchmarkCoreProjectionDensity(b *testing.B) {
	for name, node := range projectionNodes() {
		b.Run(name, func(b *testing.B) {
			want, err := readCoreNode(node, nil, nil)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := readCoreNode(node, nil, nil)
				if err != nil || normalizedOutputBytes(got) != normalizedOutputBytes(want) {
					b.Fatal("projection changed", err)
				}
			}
		})
	}
}
