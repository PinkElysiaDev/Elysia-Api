package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Measures the authored target program while checking all terminal probabilities.
func BenchmarkAlgorithmTargetLogprobs(b *testing.B) {
	compiled := testCompiled(b, Responses)
	probabilities, err := p.ParseValue([]byte(`[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]`))
	if err != nil {
		b.Fatal(err)
	}
	created, _ := p.EncodeValue(1)
	for _, count := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			start := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": created}}}
			item := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("t"), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue("t")}}
			delta := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: item.ItemID, Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{{Codec: Responses, SourceCodec: Responses, Update: "append", Location: "content", Name: "logprobs", Value: probabilities}}}
			terminal := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}}
			b.ReportAllocs()
			for b.Loop() {
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				validation, err := compiled.NewWireStreamValidation(options.Scope)
				if err != nil {
					b.Fatal(err)
				}
				options = options.WithOutputStream(validation)
				consume := func(frames []p.Value) {
					for _, frame := range frames {
						if err := validation.Consume(b.Context(), frame); err != nil {
							b.Fatal(err)
						}
					}
					if len(frames) > 0 {
						if err := validation.CommitDelivery(); err != nil {
							b.Fatal(err)
						}
					}
				}
				encode := func(event p.Event) []p.Value {
					frames, err := compiled.EncodeFrames(b.Context(), event, options)
					if err != nil {
						b.Fatal(err)
					}
					consume(frames)
					return frames
				}
				for _, event := range []p.Event{start, item} {
					encode(event)
				}
				for range count {
					if frames := encode(delta); len(frames) != 1 {
						b.Fatal("incremental probability frame lost")
					}
				}
				encode(terminal)
				frames, err := compiled.FinishEvents(b.Context(), options)
				if err != nil || len(frames) == 0 {
					b.Fatal("terminal frame lost", err)
				}
				consume(frames)
				if err := validation.Finish(); err != nil {
					b.Fatal(err)
				}
				validation.Release()
				options.State.Release()
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
