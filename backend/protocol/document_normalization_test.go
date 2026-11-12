package protocol

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestDocumentNormalizationParity(t *testing.T) {
	for _, raw := range []string{
		`"` + strings.Repeat("A", 64<<10) + `"`,
		` { "x": "<>&\u2028" } `,
		`"\u0041"`,
		"\"\xff\"",
	} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		input := &Request{Content: []Node{{Payload: value}, {Payload: value}}, Parameters: Object{"same": value}}
		limits := semanticLimits(DefaultLimits())
		ctx := withDocumentMetrics(context.Background(), 2<<20)
		if err := checkDocumentSemantic(ctx, input, limits); err != nil {
			t.Fatal(err)
		}
		got := normalizeDocumentMapping(ctx, input, limits)
		want := normalizeMappingSemantic(input, limits)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("normalization differs")
		}
		got.Content[0].Payload = StringValue("changed")
		got.Parameters["same"] = StringValue("changed")
		if input.Content[0].Payload != value || input.Parameters["same"] != value {
			t.Fatal("normalization shares mutable containers")
		}
		tight := limits
		tight.BufferBytes = 1
		gotErr := checkDocumentSemantic(ctx, input, tight)
		wantErr := checkSemanticValue(input, tight)
		if gotErr == nil || wantErr == nil || gotErr.Error() != wantErr.Error() {
			t.Fatal("normalization changed budget failures", gotErr, wantErr)
		}
	}
}

func BenchmarkDocumentNormalizationPipeline(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{{"short", 256}, {"image2", 2 << 20}} {
		b.Run(size.name, func(b *testing.B) {
			value := StringValue(strings.Repeat("A", size.bytes))
			request := &Request{Content: []Node{{Payload: value}, {Payload: value}}, Native: &Native{Value: value}}
			limits := semanticLimits(DefaultLimits())
			b.ReportAllocs()
			for b.Loop() {
				ctx := withDocumentMetrics(context.Background(), value.Len())
				if err := checkDocumentSemantic(ctx, request, limits); err != nil {
					b.Fatal(err)
				}
				normalized := normalizeDocumentMapping(ctx, request, limits)
				if normalized.Content[0].Payload != value {
					b.Fatal("payload changed")
				}
			}
		})
	}
}

func TestDocumentNormalizationConcurrent(t *testing.T) {
	ctx := withDocumentMetrics(t.Context(), 2<<20)
	limits := semanticLimits(DefaultLimits())
	var group sync.WaitGroup
	for index := range 8 {
		group.Go(func() {
			value := StringValue(strings.Repeat(string(rune('a'+index)), 64<<10))
			input := &Request{Content: []Node{{Payload: value}, {Payload: value}}}
			for range 8 {
				if err := checkDocumentSemantic(ctx, input, limits); err != nil {
					t.Error(err)
					return
				}
				result := normalizeDocumentMapping(ctx, input, limits)
				if result.Content[0].Payload != value {
					t.Error("concurrent normalization changed payload")
				}
			}
		})
	}
	group.Wait()
}
