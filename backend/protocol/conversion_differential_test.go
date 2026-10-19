package protocol_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func performanceProtocols(t testing.TB) []*p.Compiled {
	t.Helper()
	definitions, err := builtin.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), builtin.Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var protocols []*p.Compiled
	for _, definition := range definitions {
		compiled, issues := compiler.Compile(definition.Bytes())
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		protocols = append(protocols, compiled)
	}
	return protocols
}

func differentialValue(t testing.TB, raw string) p.Value {
	t.Helper()
	value, err := p.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compareConversion(t testing.TB, conversion *p.CompiledConversion, phase p.ConversionPhase, input p.Value, route p.ConversionContext) {
	t.Helper()
	wantSink, gotSink := &p.DiagnosticSink{}, &p.DiagnosticSink{}
	want, wantErr := conversion.ApplyValueBaseline(t.Context(), phase, input, route, wantSink)
	got, gotErr := conversion.ApplyValue(t.Context(), phase, input, route, gotSink)
	var wantIssues, gotIssues *p.ConversionError
	errors.As(wantErr, &wantIssues)
	errors.As(gotErr, &gotIssues)
	if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !reflect.DeepEqual(wantIssues, gotIssues) {
		t.Fatalf("errors differ: old=%#v (%v), new=%#v (%v)", wantIssues, wantErr, gotIssues, gotErr)
	}
	if want != got {
		t.Fatalf("wire differs:\nold=%s\nnew=%s", want.Bytes(), got.Bytes())
	}
	if !reflect.DeepEqual(wantSink.Issues(), gotSink.Issues()) {
		t.Fatalf("diagnostics differ:\nold=%+v\nnew=%+v", wantSink.Issues(), gotSink.Issues())
	}
}

func TestDefaultConversionDifferential(t *testing.T) {
	protocols := performanceProtocols(t)
	catalog := map[string]bool{}
	fixtures := map[p.ConversionPhase][]string{
		p.ConversionRequest: {
			`{"schemaVersion":1,"content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}]}`,
			`{"schemaVersion":1,"parameters":{"store":false},"content":[{"kind":"reasoning","payload":"think"},{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"reply"}]},{"kind":"tool_call","callId":"c1","name":"echo","input":{"kind":"json","value":{"n":9007199254740993}}},{"kind":"tool_result","callId":"c1","payload":"result"}]}`,
			`{"schemaVersion":1,"content":[{"kind":"tool_result","callId":"c1","status":{"isError":true},"payload":{"n":9007199254740993}}]}`,
			`{"schemaVersion":1,"parameters":{},"content":[{"kind":"message","role":"\u0061ssistant","children":[],"attributes":{},"payload": { "text": "<>&\u0061", "n":1e2 }}]}`,
			`{"schemaVersion":1,"parameters":{"responses_truncation":42},"unknown":true}`,
			`{"schemaVersion":1,"content":[{"kind":"reasoning","payload":"signed","resources":[{"kind":"signature","id":"synthetic"}]}]}`,
		},
		p.ConversionResponse: {
			`{"schemaVersion":1,"id":"r","model":"m","status":"completed","content":[{"kind":"text","payload":"hello"}],"usage":{"input":{"count":2,"origin":"observed"},"output":{"count":1,"origin":"observed"}}}`,
			`{"schemaVersion":1,"id":"r","model":"m","error":{"type":"rate_limit_error","message":"busy","code":"gateway_concurrency_limit","param":""},"attributes":{}}`,
			`{"schemaVersion":1,"id":"r","model":"m","content":[],"attributes":{"escaped":"\u0061","n":9007199254740993}}`,
		},
		p.ConversionEvent: {
			`{"schemaVersion":1,"type":"item.delta","responseId":"r","itemId":"i","delta":"text"}`,
			`{"schemaVersion":1,"type":"response.started","responseId":"r","response":{"schemaVersion":1,"id":"r","model":"m"}}`,
			`{"schemaVersion":1,"type":"response.finished","responseId":"r","response":{"schemaVersion":1,"id":"r","model":"m","status":"completed","usage":{"input":{"count":2,"origin":"observed"},"output":{"count":1,"origin":"observed"}}}}`,
			`{"schemaVersion":1,"type":"metadata.updated","responsesTurnState":{"frame":{"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic"}},"source":{"protocol":{"family":"responses"},"direction":"decode_event","path":""},"frameNumber":1}}`,
		},
	}
	for _, source := range protocols {
		for _, target := range protocols {
			policy := p.DefaultConversionPolicy(source, target)
			for _, rule := range policy.Rules {
				catalog[rule.ID] = true
			}
			for _, mode := range []string{"compatible", "strict"} {
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m", Delivery: p.NewDeliveryState()}
				for phase, samples := range fixtures {
					for index, raw := range samples {
						t.Run(fmt.Sprintf("%s/%s/%s/%s/%d", source.Codec(p.EncodeRequest), target.Codec(p.EncodeRequest), mode, phase, index), func(t *testing.T) {
							compareConversion(t, conversion, phase, differentialValue(t, raw), route)
						})
					}
				}
			}
		}
	}
	if len(catalog) != 31 {
		t.Fatalf("default rule catalog changed: %d, want 31", len(catalog))
	}
}
