package builtin

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

type observedRequestModule struct {
	module
	decodes *atomic.Int64
}

func (adapter observedRequestModule) DecodeRequest(ctx context.Context, value p.Value, options p.EvaluationContext) (*p.Request, error) {
	adapter.decodes.Add(1)
	return adapter.module.DecodeRequest(ctx, value, options)
}

func TestRequestWorkspaceIsolationAndInvalidation(t *testing.T) {
	var decodes atomic.Int64
	installed := Modules()
	for index, entry := range installed {
		if entry.Name() == Anthropic {
			installed[index] = observedRequestModule{entry.(module), &decodes}
		}
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), installed, nil)
	definitions, _ := Definitions()
	var compiled *p.Compiled
	for _, definition := range definitions {
		current, issues := compiler.Compile(definition.Bytes())
		if current == nil {
			t.Fatal(issues)
		}
		if current.Codec(p.DecodeRequest) == Anthropic {
			compiled = current
		}
	}
	body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"original"}],"unknown":9007199254740993}`)
	options := p.EvaluationContext{Values: p.Object{"path": p.StringValue("/v1/messages")}}
	options.ResolveRequestScope = func(request *p.Request) (p.Scope, error) {
		// Authorization callbacks may mutate their input. Replay must use the
		// module result before this callback, and must never invoke it again.
		request.Model = p.StringValue("authorized-model")
		return p.Scope{}, nil
	}
	request, err := compiled.DecodeRequest(t.Context(), body, options)
	if err != nil {
		t.Fatal(err)
	}
	if decodes.Load() != 1 {
		t.Fatal("unexpected initial decode count")
	}
	options.ResolveRequestScope = func(*p.Request) (p.Scope, error) {
		t.Error("scope callback was replayed")
		return p.Scope{}, nil
	}
	var workers sync.WaitGroup
	for index := range 8 {
		workers.Go(func() {
			candidate := request.Clone()
			candidate.Model = p.StringValue(strings.Repeat("x", index+1))
			wire, err := compiled.EncodeRequest(t.Context(), candidate, options)
			if err != nil || !strings.Contains(string(wire), "9007199254740993") {
				t.Errorf("candidate lost native evidence: %v", err)
			}
		})
	}
	workers.Wait()
	if decodes.Load() != 1 || !request.Model.IsString("authorized-model") {
		t.Fatal("baseline was re-decoded or caller was changed")
	}
	// The JSON boundary intentionally drops private workspaces. Its output
	// must remain byte-identical, including the callback's semantic edits.
	raw, _ := sonic.ConfigStd.Marshal(request)
	var uncached p.Request
	if err := sonic.ConfigStd.Unmarshal(raw, &uncached); err != nil {
		t.Fatal(err)
	}
	before, beforeErr := compiled.EncodeRequest(t.Context(), &uncached, options)
	after, afterErr := compiled.EncodeRequest(t.Context(), request, options)
	if beforeErr != nil || afterErr != nil || string(before) != string(after) {
		t.Fatalf("workspace changed replay: %s / %s; %v / %v", before, after, beforeErr, afterErr)
	}
	count := decodes.Load()
	definition, _ := sonic.ConfigStd.Marshal(compiled.Definition())
	independent, issues := compiler.Compile(definition)
	if independent == nil {
		t.Fatal(issues)
	}
	if _, err := independent.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatal(err)
	}
	if decodes.Load() != count+1 {
		t.Fatal("independently compiled registry reused a foreign workspace")
	}
	count = decodes.Load()
	request.Native.RequestContext.Values["path"] = p.StringValue("changed-context")
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatal(err)
	}
	if decodes.Load() != count+1 {
		t.Fatal("changed source context reused stale baseline")
	}
	count = decodes.Load()
	changed, _ := p.ParseValue([]byte(strings.Replace(string(body), "original", "changed", 1)))
	request.Native.Value = changed
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatal(err)
	}
	if decodes.Load() != count+1 {
		t.Fatal("changed snapshot reused stale baseline")
	}
}
