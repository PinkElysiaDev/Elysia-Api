package protocol

import (
	"fmt"
	"testing"
)

// Full conversion cost, including clone, initial normalization and every rule
// boundary. This fixture compiles unchanged against the previous implementation.
func BenchmarkAlgorithmConversion(b *testing.B) {
	for _, nodes := range []int{64, 256, 1024} {
		for _, passes := range []int{1, 4, 16} {
			b.Run(fmt.Sprintf("nodes%d/rules%d", nodes, passes), func(b *testing.B) {
				request := &Request{SchemaVersion: 1, Parameters: Object{"stream": {raw: "true"}}}
				for range nodes {
					request.Content = append(request.Content, Node{Kind: TextNode, Payload: StringValue("history")})
				}
				var rules []ConversionRule
				for i := range passes {
					rules = append(rules, ConversionRule{ID: fmt.Sprint("rule", i), Order: i, Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic")})
				}
				c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "scaling", Mode: "compatible", Rules: rules})
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					out, err := c.Request(b.Context(), request, ConversionContext{}, nil)
					if err != nil || len(out.Content) != nodes {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
