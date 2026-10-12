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
	return s.saveSourceChanges(ctx, storage.BoundSourceUpdate{Source: source})
}

func (s *Server) saveSourceChanges(ctx context.Context, update storage.BoundSourceUpdate) (err error) {
	source := update.Source
	defer func() {
		if err == nil {
			s.noteSourceSaved(source.ID, source.AutoFetchModels && source.Platform != "")
		}
	}()
	policies, bindings, generation, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return err
	}
	binding := storage.ProtocolBinding{Kind: "source", SourceID: source.ID, Unbound: true}
	if source.Platform == "" {
		source.AutoFetchModels = false
	} else {
		compiled, err := s.sourceProtocol(source)
		if err != nil {
			return err
		}
		binding = makeProtocolBinding(binding.Key(), compiled)
		for _, current := range bindings {
			if current.Key() == binding.Key() && !current.Unbound && current.Binding.ProtocolID == compiled.Identity().DefinitionID {
				binding = current
				break
			}
		}
		if err := protocol.IssuesError(protocol.CheckBinding(binding.Binding, compiled)); err != nil {
			return err
		}
	}
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	updates, err := s.prepareBindingUpdate(ctx, service.View(), policies, bindings, binding)
	if err != nil {
		return err
	}
	update.Source, update.Bindings = source, updates
	return s.store.SaveBoundSource(ctx, update, generation)
}
