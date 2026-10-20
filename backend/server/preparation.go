package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/admission"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/gin-gonic/gin"
)

const preparationLeaseKey = "elysiaPreparationLease"

func (s *Server) preparationLimiter() *admission.Limiter {
	s.preparationOnce.Do(func() {
		cfg := s.config
		if cfg == nil {
			cfg = &config.Config{}
		}
		limits := cfg.GetPreparationLimits()
		s.preparation = admission.New(admission.Limits{Enabled: limits.Enabled, MaxConcurrent: limits.MaxConcurrent, MaxQueued: limits.MaxQueued, MaxReservedBodyBytes: limits.MaxReservedBodyBytes, QueueTimeout: limits.QueueTimeout})
	})
	return s.preparation
}

func (s *Server) acquireGatewayPreparation(c *gin.Context, bytes int64) error {
	if value, exists := c.Get(preparationLeaseKey); exists && value != nil {
		return nil
	}
	lease, err := s.preparationLimiter().Acquire(c.Request.Context(), bytes)
	if err == nil {
		c.Set(preparationLeaseKey, lease)
	}
	return err
}

func (s *Server) releaseGatewayPreparation(c *gin.Context) {
	if value, exists := c.Get(preparationLeaseKey); exists && value != nil {
		value.(*admission.Lease).Release()
		c.Set(preparationLeaseKey, nil)
	}
}

func preparationStatus(err error) int {
	if errors.Is(err, admission.ErrCapacity) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusServiceUnavailable
}

func isPreparationFailure(err error) bool {
	return errors.Is(err, admission.ErrFull) || errors.Is(err, admission.ErrTimeout) || errors.Is(err, admission.ErrClosed) || errors.Is(err, admission.ErrCapacity)
}

func (s *Server) rejectPreparation(c *gin.Context, compiled *protocol.Compiled, err error) {
	status := preparationStatus(err)
	if status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "1")
	}
	record := s.initUsageRecord(c, time.Now(), nil, builtin.FormatType(compiled.Identity().Family))
	record.IngressRevision, record.SourceFormat, record.RelayMode = compiled.Hash(), compiled.Identity().DefinitionID, "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	s.failGateway(c, record, status, err)
	s.recordUsage(record)
}
