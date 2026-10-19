package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatClientOutputContract(t *testing.T) {
	for _, declarativePath := range []bool{false, true} {
		compiled := compileAgentDefinition(t, func(d *p.Definition) {
			if declarativePath {
				m := d.Directions[p.DecodeRequest]
				m.After = &p.Expression{Op: "read", Path: ""}
				d.Directions[p.DecodeRequest] = m
			}
		})
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[]}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if request.ClientOutput == nil || request.ClientOutput.IncludeUsage == nil || !*request.ClientOutput.IncludeUsage || !request.Parameters["stream_options"].IsZero() {
			t.Fatal("client output was not separated from generation parameters", request)
		}
	}
}
