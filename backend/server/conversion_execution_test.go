package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

// Measurement chooses an execution path explicitly. Production selection never
// installs test packages: it must prove that the actual defaults use packages.
func conversionExecution(t *testing.T) string {
	t.Helper()
	selection := os.Getenv("ELYSIA_CONVERSION_HTTP_EXECUTION")
	switch selection {
	case "legacy", "packages", "production":
		return selection
	default:
		t.Fatal("ELYSIA_CONVERSION_HTTP_EXECUTION must select legacy, packages or production")
		return ""
	}
}

func conversionPackage(t *testing.T, id string) *protocol.Compiled {
	t.Helper()
	name := map[string]string{
		protocol.PresetChatCompletionsID: "openai-chat", protocol.PresetResponsesID: "openai-responses",
		protocol.PresetAnthropicID: "anthropic", protocol.PresetGeminiID: "google-generate-content",
	}[id]
	if name == "" {
		t.Fatal("unknown measurement protocol", id)
	}
	read := func(part string) []byte {
		raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "packages", name, part+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(read("spec"), read("mapping"))
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

type conversionExecutionProof struct {
	Execution, Commit, Compiler string
	Packages                    map[string]string
	Policies                    map[string]string
}

func configureConversionExecution(t *testing.T, server *Server, groups []config.ModelGroupConfig) {
	t.Helper()
	if conversionExecution(t) == "packages" {
		seedGatewayPackages(t, server)
		seedRoutingGroups(t, server, groups)
	}
}

func conversionExecutionIdentity(t *testing.T, server *Server) *conversionExecutionProof {
	t.Helper()
	proof := &conversionExecutionProof{Execution: conversionExecution(t), Commit: AppCommit,
		Compiler: protocol.CompilerVersion, Packages: map[string]string{}, Policies: map[string]string{}}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	view := service.View()
	for _, id := range []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID} {
		compiled, ok := view.Pin(id)
		if !ok {
			t.Fatal("measurement protocol is not active", id)
		}
		for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest, protocol.DecodeResponse, protocol.EncodeResponse, protocol.DecodeEvent, protocol.EncodeEvent} {
			if (compiled.Codec(direction) == "") != (proof.Execution != "legacy") {
				t.Fatal("measurement execution selection does not match active protocol", id, direction)
			}
		}
		proof.Packages[id] = compiled.Hash()
	}
	bindings, err := server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if proof.Packages[binding.Binding.ProtocolID] != binding.Binding.RevisionHash {
			t.Fatal("measurement binding does not select the active revision", binding.Binding.ProtocolID)
		}
		for _, report := range binding.Combinations {
			if report.Passed {
				proof.Policies[report.SourceHash+":"+report.TargetHash+":"+report.ContextHash] = report.PolicyHash
			}
		}
	}
	if len(proof.Policies) == 0 {
		t.Fatal("measurement has no verified conversion policies")
	}
	return proof
}
