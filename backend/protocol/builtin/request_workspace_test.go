package builtin

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestRequestWorkspaceIsolationAndInvalidation(t *testing.T) {
	const workers = 8
	compiled := testCompiled(t, Anthropic)
	var authorizations atomic.Int64
	options := p.EvaluationContext{Values: p.Object{"path": p.StringValue("/v1/messages")}, ResolveRequestScope: func(request *p.Request) (p.Scope, error) {
		authorizations.Add(1)
		request.Model = p.StringValue("authorized-model")
		return p.Scope{}, nil
	}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"original"}],"unknown":9007199254740993}`), options)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := range workers {
		group.Go(func() {
			candidate := request.Clone()
			model := strings.Repeat("x", index+1)
			candidate.Model = p.StringValue(model)
			wire, err := compiled.EncodeRequestValue(t.Context(), candidate, options)
			if err != nil {
				t.Error(err)
				return
			}
			fields, err := wire.ReadObject()
			if err != nil || !fields["model"].IsString(model) || fields["unknown"].JSONString() != "9007199254740993" {
				t.Error("candidate lost its edit or native evidence", wire, err)
			}
		})
	}
	group.Wait()
	after, err := p.EncodeValue(request)
	if err != nil || baseline != after || authorizations.Load() != 1 {
		t.Fatal("candidate encoding mutated history or repeated authorization", authorizations.Load(), err)
	}
	var serialized p.Request
	if err := baseline.Decode(&serialized); err != nil {
		t.Fatal(err)
	}
	before, err := compiled.EncodeRequest(t.Context(), &serialized, options)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequest(t.Context(), request, options)
	if err != nil || !bytes.Equal(before, wire) || authorizations.Load() != 1 {
		t.Fatal("private workspace changed replay or repeated authorization", string(before), string(wire), err)
	}
}
