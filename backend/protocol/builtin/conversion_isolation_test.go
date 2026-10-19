package builtin

import (
	"fmt"
	"sync"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Retain the concurrency regression from the retired optimization differential
// suite, asserting source ownership directly rather than a frozen implementation.
func TestConcurrentOwnedRequestIsolation(t *testing.T) {
	t.Parallel()
	compiled := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	input, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"unchanged"}],"extra":9007199254740993}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.EncodeValue(input)
	var wait sync.WaitGroup
	for index := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			out, err := conversion.Request(t.Context(), input, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
			if err != nil {
				t.Error(err)
				return
			}
			out.Content[0].Children[0].Payload = p.StringValue(fmt.Sprint(index))
			out.Native.Source.Path = "private"
			if out.Parameters == nil {
				out.Parameters = p.Object{}
			}
			out.Parameters["private"] = p.StringValue(fmt.Sprint(index))
		}()
	}
	wait.Wait()
	after, _ := p.EncodeValue(input)
	if before != after {
		t.Fatal("concurrent conversion mutated source")
	}
}
