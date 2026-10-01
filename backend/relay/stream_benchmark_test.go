package relay

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkStreamTextDecode measures prefix tracking on a sustained text stream.
func BenchmarkStreamTextDecode(b *testing.B) {
	frame := SSEEvent{Data: fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, strings.Repeat("x", 256))}
	finish := SSEEvent{Data: `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`}
	b.ReportAllocs()
	b.SetBytes(256 * 256)
	for b.Loop() {
		decoder := NewMaheshvaraStreamDecoder(FormatOpenAIChat)
		for range 256 {
			if _, err := decoder.Decode(frame); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := decoder.Decode(finish); err != nil {
			b.Fatal(err)
		}
	}
}
