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
	for _, variant := range []string{"same", "model", "scope", "budget", "cache", "thinking", "cancel", "content"} {
		t.Run(variant, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`), protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			warning := protocol.ConversionIssue{Severity: protocol.SeverityWarning, Reason: "preflight diagnostic"}
			candidate := gatewayCandidate{compiled: compiled, encoding: &gatewayRequestEncoding{body: []byte("prepared"), request: request, identity: compiled.Identity(), limits: compiled.RequestLimits(t.Context()), issues: []protocol.ConversionIssue{warning}}}
			actual := request.Clone()
			options := protocol.EvaluationContext{Diagnostics: &protocol.DiagnosticSink{}}
			ctx := t.Context()
			switch variant {
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
			body, ok := candidate.takeRequestEncoding(ctx, actual, options)
			if ok != (variant == "same") {
				t.Fatalf("incorrect reuse: %t", ok)
			}
			if ok && (string(body) != "prepared" || len(options.Diagnostics.Issues()) != 1) || !ok && len(options.Diagnostics.Issues()) != 0 {
				t.Fatal("diagnostics published at the wrong boundary")
			}
			if candidate.encoding.body != nil || candidate.encoding.request != nil {
				t.Fatal("consumed or invalidated encoding retained payload")
			}
			if _, reused := candidate.takeRequestEncoding(ctx, actual, options); reused {
				t.Fatal("encoding consumed twice")
			}
		})
	}
}
