package server

import (
	"context"
	"strings"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) validateSourceProtocol(source *storage.ModelSource) error {
	platform := strings.TrimSpace(source.Platform)
	if platform == "" {
		source.Platform = ""
		source.AutoFetchModels = false
		return nil
	}
	if len(platform) >= len("custom:") && strings.EqualFold(platform[:len("custom:")], "custom:") {
		source.Platform = "custom:" + platform[len("custom:"):]
	} else {
		source.Platform = storage.NormalizePlatform(strings.ToLower(platform))
	}
	compiled, err := s.sourceProtocol(*source)
	if err != nil {
		return err
	}
	if source.AutoFetchModels {
		_, _, err = selectModelDiscovery(compiled)
	}
	return err
}

// saveSource installs a verified source contract once. Subsequent metadata
// edits preserve the operator's capabilities, operation and waiting policy.
func (s *Server) saveSource(ctx context.Context, source storage.ModelSource) error {
	if source.Platform == "" {
		source.AutoFetchModels = false
		return s.store.SaveBoundSource(ctx, source, storage.ProtocolBinding{Kind: "source", SourceID: source.ID, Unbound: true})
	}
	compiled, err := s.sourceProtocol(source)
	if err != nil {
		return err
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return err
	}
	binding := makeProtocolBinding(upgradeBindingKey{kind: "source", source: source.ID}, compiled)
	for _, current := range bindings {
		if current.SourceID != source.ID {
			continue
		}
		if current.Kind == "source" && current.Binding.ProtocolID == compiled.Identity().DefinitionID {
			binding = current
		}
	}
	if err := protocol.IssuesError(protocol.CheckBinding(binding.Binding, compiled)); err != nil {
		return err
	}
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	binding.Combinations = verifyGatewayBinding(ctx, service.View(), compiled, binding.Binding.Capabilities)
	if !hasPassingGatewayCombination(binding.Combinations) {
		return gatewayIssue(compiled.Identity(), protocol.VerificationRequired, "/binding/combinations", "source requires a verified ingress conversion contract")
	}
	return s.store.SaveBoundSource(ctx, source, binding)
}
