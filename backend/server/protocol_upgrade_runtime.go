package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

// initializeProtocolRuntime makes migration all-or-nothing for serving. A
// failed preview leaves the management API usable and never selects old codecs.
func (s *Server) initializeProtocolRuntime(ctx context.Context) (err error) {
	defer func() { s.recordProtocolRuntimeFailure(err) }()
	if err := s.recoverRequiredPresets(ctx); err != nil {
		return err
	}
	return s.completeProtocolRuntimeInitialization(ctx)
}

// Recover presets independently of legacy custom migration. Publishing this
// verified snapshot does not open the generation gate or write a legacy receipt.
func (s *Server) recoverRequiredPresets(ctx context.Context) error {
	s.isProtocolRuntimeRequired.Store(true)
	s.protocolRuntimeReady.Store(false)
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	if err := s.refreshProtocolRuntimeScope(ctx, service, true); err != nil {
		return err
	}
	if err := service.ReloadAvailable(ctx, requiredPresetIDs...); err != nil {
		return err
	}
	return checkRequiredPresets(service.View())
}

func checkRequiredPresets(view protocol.RegistryView) error {
	for _, id := range requiredPresetIDs {
		if _, ok := view.Pin(id); !ok {
			return fmt.Errorf("preset %s runtime publication failed", id)
		}
	}
	return nil
}

func (s *Server) completeProtocolRuntimeInitialization(ctx context.Context) error {
	receipt, err := s.store.ProtocolUpgradeStatus(ctx)
	if err != nil {
		return err
	}
	if receipt == nil {
		if _, err := s.applyProtocolUpgrade(ctx, protocolUpgradeInput{}); err != nil {
			return err
		}
	}
	return s.reloadProtocolRuntime(ctx)
}

func (s *Server) reloadProtocolRuntime(ctx context.Context) (err error) {
	defer func() { s.recordProtocolRuntimeFailure(err) }()
	s.protocolRuntimeReady.Store(false)
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	if err := s.refreshProtocolRuntime(ctx, service); err != nil {
		return err
	}
	if err := service.ReloadAvailable(ctx, requiredPresetIDs...); err != nil {
		return err
	}
	if err := checkRequiredPresets(service.View()); err != nil {
		return err
	}
	if s.isProtocolRuntimeRequired.Load() {
		receipt, err := s.store.ProtocolUpgradeStatus(ctx)
		if err != nil {
			return err
		}
		if receipt == nil {
			return fmt.Errorf("complete protocol migration before enabling generation")
		}
		s.protocolRuntimeReady.Store(true)
	}
	return nil
}

func (s *Server) recordProtocolRuntimeFailure(err error) {
	if err == nil {
		s.protocolRuntimeFailure.Store(nil)
		return
	}
	message := err.Error()
	s.protocolRuntimeFailure.Store(&message)
}

func (s *Server) protocolRuntimeError() error {
	if s.isProtocolRuntimeRequired.Load() && !s.protocolRuntimeReady.Load() {
		return gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/migration", "protocol migration or current-engine verification requires repair; use the protocol migration preview")
	}
	return nil
}

func (s *Server) requireProtocolRuntime(c *gin.Context) bool {
	if err := s.protocolRuntimeError(); err != nil {
		respondFail(c, http.StatusServiceUnavailable, "protocol_migration_required", err.Error())
		return false
	}
	return true
}
