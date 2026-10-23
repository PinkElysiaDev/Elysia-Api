package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The baseline already exposes these event methods; this fixture does not
// depend on deferred-state internals and can run unchanged on both revisions.
func BenchmarkAlgorithmTargetLogprobs(b *testing.B) {
	probabilities, err := p.ParseValue([]byte(`[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]`))
	if err != nil {
		b.Fatal(err)
	}
	created, _ := p.EncodeValue(1)
	for _, count := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			start := p.Event{SchemaVersion: 1, Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": created}}}
			item := p.Event{SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue("t"), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue("t")}}
			delta := p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: item.ItemID, Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{{Codec: Responses, Location: "content", Name: "logprobs", Value: probabilities}}}
			terminal := p.Event{SchemaVersion: 1, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}}
			b.ReportAllocs()
			for b.Loop() {
				instance, err := (module{name: Responses}).NewStream(p.EncodeEvent, p.DefaultLimits())
				if err != nil {
					b.Fatal(err)
				}
				stream := instance.(*streamModule)
				for _, event := range []p.Event{start, item} {
					if _, err := stream.EncodeEventFrames(b.Context(), event, p.EvaluationContext{}); err != nil {
						b.Fatal(err)
					}
				}
				for range count {
					frames, err := stream.EncodeEventFrames(b.Context(), delta, p.EvaluationContext{})
					if err != nil || len(frames) != 1 {
						b.Fatal("incremental probability frame lost", err)
					}
				}
				if _, err := stream.EncodeEventFrames(b.Context(), terminal, p.EvaluationContext{}); err != nil {
					b.Fatal(err)
				}
				frames, err := stream.Finish(b.Context(), p.EvaluationContext{})
				if err != nil || len(frames) == 0 {
					b.Fatal("terminal frame lost", err)
				}
				fields, _ := frames[len(frames)-1].ReadObject()
				response, _ := fields["response"].ReadObject()
				var output, content, entries []p.Value
				if response["output"].Decode(&output) != nil || len(output) != 1 {
					b.Fatal("terminal output item lost")
				}
				message, _ := output[0].ReadObject()
				if message["content"].Decode(&content) != nil || len(content) != 1 {
					b.Fatal("terminal output content lost")
				}
				part, _ := content[0].ReadObject()
				if part["logprobs"].Decode(&entries) != nil || len(entries) != count {
					b.Fatal("terminal probability entries lost")
				}
			}
		})
	}
}
