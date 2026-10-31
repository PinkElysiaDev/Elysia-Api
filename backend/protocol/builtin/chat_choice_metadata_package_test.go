package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageChoiceMetadataWithoutNative(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	for _, raw := range []string{
		`{"id":"r","created":1,"model":"m","choices":[{"index":2,"message":{"role":"assistant","content":"first"},"finish_reason":"stop","logprobs":{"content":[{"token":"first","logprob":-1}]},"native_finish_reason":"provider_stop","vendor_choice":{"n":9007199254740993}},{"index":4,"message":{"role":"assistant","content":null,"refusal":"denied"},"finish_reason":"length","logprobs":{"refusal":[{"token":"denied","logprob":-2}]},"native_finish_reason":null,"vendor_choice":"two"}]}`,
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop","logprobs":{"content":[{"token":"OK","logprob":-1}],"future":{"n":9007199254740993}}}]}`,
	} {
		response, err := compiled.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		response.Native = nil
		for index := range response.Content {
			response.Content[index].Native = nil
			for child := range response.Content[index].Children {
				response.Content[index].Children[child].Native = nil
			}
		}
		wire, err := compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		before, err := testValue(t, raw).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		after, err := testValue(t, string(wire)).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, after["choices"].Bytes(), before["choices"].JSONString())
		if len(response.Content) == 2 {
			for index, kind := range []p.NodeKind{p.TextNode, p.RefusalNode} {
				hasProbability := false
				for _, child := range response.Content[index].Children {
					for _, metadata := range child.Metadata {
						if metadata.Name == "logprobs" {
							hasProbability = child.Kind == kind && metadata.Location == "content" && metadata.Update == "" && metadata.Value.IsArray()
						}
					}
				}
				if !hasProbability {
					t.Fatal("choice probabilities lost their text or refusal owner", response.Content[index])
				}
			}
		}
	}
}
