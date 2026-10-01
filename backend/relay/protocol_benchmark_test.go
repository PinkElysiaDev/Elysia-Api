package relay

import "testing"

// BenchmarkProtocolConversion measures the complete parse/render boundary used
// by the gateway, including tool history and cache intent.
func BenchmarkProtocolConversion(b *testing.B) {
	body := []byte(cacheChatFixture)
	for _, target := range []FormatType{FormatOpenAIChat, FormatClaude, FormatGemini, FormatResponses} {
		b.Run(string(target), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				req, err := OpenAIChatToMaheshvara(body)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := MaheshvaraToTargetRequest(req, target, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
