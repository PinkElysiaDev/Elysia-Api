package server

import (
	"context"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestGatewayPreparedEncodingBoundaries(t *testing.T) {
	t.Parallel()
	compiler, _ := protocol.NewCompiler(protocol.DefaultLimits(), builtin.Modules(), nil)
	definitions, _ := builtin.Definitions()
	var compiled *protocol.Compiled
	for _, raw := range definitions {
		entry, _ := compiler.Compile(raw.Bytes())
		if entry.Identity().DefinitionID == protocol.PresetAnthropicID {
			compiled = entry
		}
	}
	for _, compiled := range []*protocol.Compiled{compiled, conversionPackage(t, protocol.PresetAnthropicID)} {
		if !compiled.CanReuseRequestEncoding() {
			t.Fatal("pure request mapping cannot reuse encoding")
		}
		for _, variant := range []string{"same", "model", "scope", "budget", "cache", "thinking", "cancel", "content", "revision", "operation", "started", "context", "output"} {
			t.Run(variant, func(t *testing.T) {
				request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`), protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				warning := protocol.ConversionIssue{Severity: protocol.SeverityWarning, Reason: "preflight diagnostic"}
				preflight, err := (protocol.EvaluationContext{Diagnostics: &protocol.DiagnosticSink{}}).WithOperation("execution", 1)
				if err != nil {
					t.Fatal(err)
				}
				preflight.Diagnostics.Add(warning)
				candidate := gatewayCandidate{compiled: compiled, encoding: newGatewayRequestEncoding(t.Context(), compiled, request, protocol.StringValue("prepared"), preflight)}
				actual := request.Clone()
				options, _ := (protocol.EvaluationContext{Diagnostics: &protocol.DiagnosticSink{}}).WithOperation("execution", 1)
				ctx := t.Context()
				switch variant {
				case "revision":
					revision := *compiled
					candidate.compiled = &revision
				case "operation":
					options, _ = options.WithOperation("other", 1)
				case "started":
					options, _ = options.WithOperation("execution", 2)
				case "context":
					options.Values = protocol.Object{"path": protocol.StringValue("changed")}
				case "output":
					options.ClientOutput = &protocol.ClientOutput{}

				case "model":
					actual.Model = protocol.StringValue("changed")
				case "scope":
					options.Scope.Account = "different"
				case "budget":
					ctx, _ = protocol.WithRequestBodyLimit(ctx, 16<<20)
				case "cache":
					candidate.model.CacheSynthesis = true
				case "thinking":
					options.InterleavedThinking = true
				case "cancel":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "content":
					actual.Content[0].Children[0].Payload = protocol.StringValue("edited")
				}
				body, ok := candidate.takeRequestValue(ctx, actual, options)
				if ok != (variant == "same") {
					t.Fatalf("incorrect reuse: %t", ok)
				}
				if ok && (!body.IsString("prepared") || len(options.Diagnostics.Issues()) != 1) || !ok && len(options.Diagnostics.Issues()) != 0 {
					t.Fatal("diagnostics published at the wrong boundary")
				}
				if !candidate.encoding.value.IsZero() || candidate.encoding.request != nil {
					t.Fatal("consumed or invalidated encoding retained payload")
				}
				if _, reused := candidate.takeRequestValue(ctx, actual, options); reused {
					t.Fatal("encoding consumed twice")
				}
			})
		}
	}
}
