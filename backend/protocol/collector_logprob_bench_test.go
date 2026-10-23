package protocol

import (
	"fmt"
	"testing"
)

// This fixture uses only the pre-optimization public collector contract so it
// can be overlaid on the baseline without copying the optimized implementation.
func BenchmarkAlgorithmCollectorLogprobs(b *testing.B) {
	probabilities, err := ParseValue([]byte(`{"content":[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]}`))
	if err != nil {
		b.Fatal(err)
	}
	for _, frames := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(frames), func(b *testing.B) {
			start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}}
			delta := Event{SchemaVersion: 1, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue("x"), Metadata: []ResponseMetadata{{Codec: "openai-chat", Location: "choice", Name: "logprobs", Value: probabilities}}}
			b.ReportAllocs()
			for b.Loop() {
				collector, err := NewResponseCollector(replayTarget(), DefaultLimits())
				if err != nil {
					b.Fatal(err)
				}
				if _, _, err := collector.Consume(start); err != nil {
					b.Fatal(err)
				}
				for range frames {
					if _, _, err := collector.Consume(delta); err != nil {
						b.Fatal(err)
					}
				}
				for _, event := range []Event{{SchemaVersion: 1, Type: ItemFinished, ItemID: start.ItemID}, {SchemaVersion: 1, Type: ResponseFinished}} {
					if _, _, err := collector.Consume(event); err != nil {
						b.Fatal(err)
					}
				}
				response, err := collector.Finish()
				if err != nil || len(response.Content) != 1 || len(response.Content[0].Metadata) != 1 {
					b.Fatal("incomplete response", err)
				}
				fields, err := response.Content[0].Metadata[0].Value.ReadObject()
				var entries []Value
				if err != nil || fields["content"].Decode(&entries) != nil || len(entries) != frames {
					b.Fatal("probability entries lost", err)
				}
			}
		})
	}
}
