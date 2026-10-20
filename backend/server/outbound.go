package server

import (
	"context"

	"github.com/elysia-api/backend/internal/outbound"
)

// syncOutboundPolicy publishes startup and runtime changes to all HTTP clients.
func (s *Server) syncOutboundPolicy() {
	policy := s.config.GetOutboundConfig()
	outbound.SetOutboundProxyURL(policy.ProxyURL)
	ranges := policy.DeniedIPRanges
	if ranges == nil {
		ranges = outbound.DefaultDeniedIPRanges
	}
	outbound.SetDeniedIPRanges(ranges)
}

func (s *Server) validateOutbound(raw string) error {
	return s.validateOutboundContext(context.Background(), raw)
}

func (s *Server) validateOutboundContext(ctx context.Context, raw string) error {
	if s.skipOutboundValidation {
		return nil
	}
	return outbound.ValidateBaseURL(ctx, raw)
}
