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
func (s *Server) initializeProtocolRuntime(ctx context.Context) error {
	s.isProtocolRuntimeRequired.Store(true)
	s.protocolRuntimeReady.Store(false)
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

func (s *Server) reloadProtocolRuntime(ctx context.Context) error {
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	if err := s.refreshProtocolRuntime(ctx, service); err != nil {
		return err
	}
	if err := service.ReloadAvailable(ctx); err != nil {
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
