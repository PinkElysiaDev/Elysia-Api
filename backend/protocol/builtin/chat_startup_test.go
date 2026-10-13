package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const startupPair = `[{"id":"a","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"system_fingerprint":null,"usage":null},{"id":"b","model":"m","created":81,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}],"service_tier":"auto"}]`

func TestChatStartupCustomModulesAndPolicy(t *testing.T) {
	base := shippedProjectionProtocol(t, Chat).Definition()
	base.ID = "old-chat-copy"
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	compile := func(d p.Definition) *p.Compiled {
		t.Helper()
		raw, _ := p.EncodeValue(d)
		c, issues := compiler.Compile(raw.Bytes())
		if c == nil {
			t.Fatal(issues)
		}
		return c
	}
	custom := compile(base)
	for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
		target := shippedProjectionProtocol(t, targetCodec)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			policy := p.DefaultConversionPolicy(target, custom)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].Action == "chat_stream_identity" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			state := p.NewSourceEventState(conversion, p.ConversionContext{Source: custom.Identity(), Target: target.Identity()}, custom, target, sink)
			output, err := state.Sequence(t.Context(), testValue(t, startupPair))
			if mode == "strict" {
				if err == nil || !strings.Contains(err.Error(), "/id") {
					t.Fatal(output, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			var frames []p.Value
			_ = output.Decode(&frames)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			for _, frame := range frames {
				_, err = custom.DecodeFrame(t.Context(), frame, options)
				if err != nil {
					break
				}
			}
			if mode == "disabled" {
				if err == nil || len(sink.Issues()) != 0 {
					t.Fatal(err, sink.Issues())
				}
				continue
			}
			if err != nil || len(sink.Issues()) != 2 {
				t.Fatal(err, sink.Issues())
			}
			third := testValue(t, `{"id":"third","model":"m","created":81,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
			batch, _ := state.Push(t.Context(), third, nil)
			if _, err := custom.DecodeFrame(t.Context(), batch[0].Value, options); err == nil || !strings.Contains(err.Error(), "frame 3") {
				t.Fatal(err)
			}
		}
	}
	base.Directions[p.EncodeEvent] = p.Mapping{Transform: &p.Expression{Op: "read"}}
	handwritten := compile(base)
	for _, rule := range p.DefaultConversionPolicy(handwritten, custom).Rules {
		if rule.Action == "chat_stream_identity" {
			t.Fatal("guessed handwritten target", rule)
		}
	}
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(custom, custom))
	state := p.NewSourceEventState(conversion, p.ConversionContext{}, custom, handwritten, nil)
	var inputs []p.Value
	_ = testValue(t, startupPair).Decode(&inputs)
	for _, input := range inputs {
		batch, err := state.Push(t.Context(), input, nil)
		if err != nil || len(batch) != 1 || batch[0].Value != input {
			t.Fatal(batch, err)
		}
	}
}

func TestStreamMetadataOwnershipOrderIsStable(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	var frames []p.Value
	_ = testValue(t, startupPair).Decode(&frames)
	var baseline p.Value
	for range 64 {
		decoded, err := compiled.DecodeFrame(t.Context(), frames[0], p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		current, _ := p.EncodeValue(decoded.Events)
		if baseline.IsZero() {
			baseline = current
		} else if current != baseline {
			t.Fatal("metadata order changed between equivalent decodes")
		}
	}
}
