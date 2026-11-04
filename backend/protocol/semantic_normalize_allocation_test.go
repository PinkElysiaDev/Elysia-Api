package protocol

import (
	"strings"
	"testing"
)

func BenchmarkSemanticNormalizationFormats(b *testing.B) {
	for name, raw := range map[string]string{
		"ascii": `"plain"`, "unicode": `"你好😀"`,
		"escaped": ` {"body":"<>&\u2028", "number":9007199254740993} `,
		"invalid": "\"\xff\"", "media": `"` + strings.Repeat("A", 2<<20) + `"`,
	} {
		b.Run(name, func(b *testing.B) {
			payload, err := ParseValue([]byte(raw))
			if err != nil {
				b.Fatal(err)
			}
			input := &Request{Content: []Node{{Kind: TextNode, Payload: payload}}, Parameters: Object{"label": payload}}
			if name == "invalid" {
				input.Parameters["\xff"] = payload
				input.Parameters["\ufffd"] = StringValue("collision")
			}
			want := normalizeMappingSemantic(input, DefaultLimits())
			b.ReportAllocs()
			for b.Loop() {
				got := normalizeMappingSemantic(input, DefaultLimits())
				if got.Content[0].Payload != want.Content[0].Payload {
					b.Fatal("normalization changed payload")
				}
			}
		})
	}
}
