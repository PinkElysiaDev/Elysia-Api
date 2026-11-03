package server

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
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
	proof, err := readConversionExecutionIdentity(t.Context(), server, conversionExecution(t))
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func readConversionExecutionIdentity(ctx context.Context, server *Server, execution string) (*conversionExecutionProof, error) {
	proof := &conversionExecutionProof{Execution: execution, Commit: AppCommit,
		Compiler: protocol.CompilerVersion, Packages: map[string]string{}, Policies: map[string]string{}}
	service, err := server.protocolService()
	if err != nil {
		return proof, err
	}
	view := service.View()
	for _, id := range []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID} {
		compiled, ok := view.Pin(id)
		if !ok {
			return proof, fmt.Errorf("measurement protocol is not active: %s", id)
		}
		proof.Packages[id] = compiled.Hash()
		for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest, protocol.DecodeResponse, protocol.EncodeResponse, protocol.DecodeEvent, protocol.EncodeEvent} {
			if (compiled.Codec(direction) == "") != (proof.Execution != "legacy") {
				return proof, fmt.Errorf("measurement execution %s does not match active protocol %s/%s (revision %s, codec %q)", execution, id, direction, compiled.Hash(), compiled.Codec(direction))
			}
		}
	}
	bindings, err := server.store.ListProtocolBindings(ctx)
	if err != nil {
		return proof, err
	}
	for _, binding := range bindings {
		if proof.Packages[binding.Binding.ProtocolID] != binding.Binding.RevisionHash {
			return proof, fmt.Errorf("measurement binding does not select the active revision: %s", binding.Binding.ProtocolID)
		}
		for _, report := range binding.Combinations {
			if report.Passed {
				proof.Policies[report.SourceHash+":"+report.TargetHash+":"+report.ContextHash] = report.PolicyHash
			}
		}
	}
	if len(proof.Policies) == 0 {
		return proof, fmt.Errorf("measurement has no verified conversion policies")
	}
	return proof, nil
}

func checkReloadedConversionExecution(ctx context.Context, server *Server, before *conversionExecutionProof) error {
	after, err := readConversionExecutionIdentity(ctx, server, before.Execution)
	if directory := os.Getenv("ELYSIA_FLAME_OUTPUT"); directory != "" {
		raw, encodeErr := json.MarshalIndent(after, "", "  ")
		if encodeErr != nil {
			return encodeErr
		}
		if writeErr := os.WriteFile(filepath.Join(directory, "reload-execution.json"), raw, 0600); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return err
	}
	if !maps.Equal(before.Packages, after.Packages) || !maps.Equal(before.Policies, after.Policies) {
		return fmt.Errorf("measurement reload changed active revisions or conversion policies")
	}
	return nil
}

func conversionProviderIdentity(t *testing.T) *conversionExecutionProof {
	t.Helper()
	return &conversionExecutionProof{Execution: conversionExecution(t), Commit: AppCommit, Compiler: protocol.CompilerVersion,
		Packages: map[string]string{}}
}

func TestConversionExecutionRejectsReloadDrift(t *testing.T) {
	groups := []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: "http://127.0.0.1:1", Platform: "claude", APIKey: "synthetic"}}}}
	server := newTestServerWithStore(t, groups)
	seedGatewayPackages(t, server)
	seedRoutingGroups(t, server, groups)
	// The collector must reject the real reload that restores shipped codecs,
	// rather than accepting its stale startup identity as package evidence.
	before, err := readConversionExecutionIdentity(t.Context(), server, "packages")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkReloadedConversionExecution(t.Context(), server, before); err != nil {
		t.Fatal(err)
	}
	if err := server.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := checkReloadedConversionExecution(t.Context(), server, before); err == nil || !strings.Contains(err.Error(), "does not match active protocol") {
		t.Fatalf("reload did not reject the execution switch: %v", err)
	}
}
