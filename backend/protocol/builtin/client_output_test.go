package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatClientOutputContract(t *testing.T) {
	compiled := testCompiled(t, Chat)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if request.ClientOutput == nil || request.ClientOutput.IncludeUsage == nil || !*request.ClientOutput.IncludeUsage || !request.Parameters["stream_options"].IsZero() {
		t.Fatal("client output was not separated from generation parameters", request)
	}
	for _, hasNative := range []bool{true, false} {
		candidate := request.Clone()
		*candidate.ClientOutput.IncludeUsage = false
		candidate.ClientOutput.RawStreamOptions = testValue(t, `{"include_usage":false}`)
		if !hasNative {
			candidate.Native = nil
		}
		wire, err := compiled.EncodeRequestValue(t.Context(), candidate, p.EvaluationContext{ClientOutput: candidate.ClientOutput})
		if err != nil {
			t.Fatal(err)
		}
		fields, err := wire.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		options, err := fields["stream_options"].ReadObject()
		if err != nil || options["include_usage"].JSONString() != "false" || !*request.ClientOutput.IncludeUsage {
			t.Fatal("client intent edit was lost or mutated its source", wire, err)
		}
	}
}
