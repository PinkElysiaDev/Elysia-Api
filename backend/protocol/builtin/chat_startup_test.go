package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const startupPair = `[{"id":"a","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"system_fingerprint":null,"usage":null},{"id":"b","model":"m","created":81,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}],"service_tier":"auto"}]`

func TestChatStartupCustomMappingsAndPolicy(t *testing.T) {
	document, _ := shippedProjectionProtocol(t, Chat).Package()
	document.Spec.ID = "custom-chat-source"
	compiler, _ := p.NewCompiler(p.DefaultLimits(), nil)
	raw, _ := p.EncodeValue(document)
	custom, issues := compiler.CompilePackage(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, targetName := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(targetName+"/"+mode, func(t *testing.T) {
				target := shippedProjectionProtocol(t, targetName)
				policy := p.DefaultConversionPolicy(target, custom)
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for index := range policy.Rules {
						if policy.Rules[index].Phase == p.ConversionSourceEvent {
							policy.Rules[index].Enabled = false
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				route := p.ConversionContext{Source: custom.Identity(), Target: target.Identity(), Transport: p.SSE}
				state := p.NewSourceEventState(conversion, route, custom, sink)
				defer state.Release()
				output, err := state.Sequence(t.Context(), testValue(t, startupPair))
				if mode == "strict" {
					if err == nil || !strings.Contains(err.Error(), "/id") || !output.IsZero() {
						t.Fatal(output, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var frames []p.Value
				_ = output.Decode(&frames)
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer options.State.Release()
				for _, frame := range frames {
					if _, err = custom.DecodeFrame(t.Context(), frame, options); err != nil {
						break
					}
				}
				if mode == "disabled" {
					if err == nil || len(sink.Issues()) != 0 {
						t.Fatal("disabled correction hid the source identity mismatch", err, sink.Issues())
					}
					return
				}
				if err != nil || len(sink.Issues()) != 2 {
					t.Fatal(err, sink.Issues())
				}
				third := testValue(t, `{"id":"third","model":"m","created":81,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
				batch, err := state.Push(t.Context(), third, nil)
				if err != nil || len(batch) != 1 {
					t.Fatal(batch, err)
				}
				if _, err := custom.DecodeFrame(t.Context(), batch[0].Value, options); err == nil || !strings.Contains(err.Error(), "/id") {
					t.Fatal("normalization disabled later identity validation", err)
				}
			})
		}
	}
}

func TestStreamMetadataOwnershipOrderIsStable(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	var frames []p.Value
	_ = testValue(t, startupPair).Decode(&frames)
	var baseline p.Value
	for range 64 {
		decoded, err := compiled.DecodeFrame(t.Context(), frames[0], fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
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
