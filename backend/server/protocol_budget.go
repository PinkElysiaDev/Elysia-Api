package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/gin-gonic/gin"
)

func (s *Server) pinProtocolBudget(ctx context.Context) (context.Context, error) {
	limit := s.config.GetMaxBodyBytes()
	if limit <= 0 {
		limit = protocol.DefaultRequestBodyBytes
	}
	return protocol.WithRequestBodyLimit(ctx, limit)
}

func (s *Server) pinProtocolRequest(c *gin.Context) bool {
	ctx, err := s.pinProtocolBudget(c.Request.Context())
	if err != nil {
		respondFail(c, http.StatusInternalServerError, "invalid_resource_config", err.Error())
		return false
	}
	c.Request = c.Request.WithContext(ctx)
	return true
}

// Preview envelopes contain semantic snapshots as well as wire input. Their
// envelope cap is separate; the contained definition and input are checked again.
func protocolPreviewPath(path string) bool {
	return strings.HasPrefix(path, "/api/admin/protocols/") && strings.HasSuffix(path, "/preview")
}

func (s *Server) readGatewayRequest(c *gin.Context, compiled *protocol.Compiled) ([]byte, bool) {
	if !s.pinProtocolRequest(c) {
		return nil, false
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, compiled.RequestLimits(c.Request.Context()).BufferBytes)
	if err == nil {
		return body, true
	}
	err = protocol.AnnotateResourceError(err, compiled.Identity(), protocol.DecodeRequest, "read.request")
	record := s.initUsageRecord(c, time.Now(), nil, builtin.FormatType(compiled.Identity().Family))
	record.IngressRevision, record.SourceFormat = compiled.Hash(), compiled.Identity().DefinitionID
	record.RelayMode = "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	s.failGateway(c, record, http.StatusBadRequest, err)
	s.recordUsage(record)
	return nil, false
}

// localResourceStatus only classifies pre-send failures; observed HTTP and
// provider failures keep their primary status and cancellation wins upstream.
func localResourceStatus(record *usageRecord, err error) int {
	var resource *protocol.ResourceLimitError
	if !errors.As(err, &resource) || record.UpstreamHTTPStatus != 0 || generationFailureClass(err) != "" {
		return 0
	}
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		for _, issue := range conversion.Issues {
			if issue.Stage == "read.request" && resource.Metric == "bytes" {
				return http.StatusRequestEntityTooLarge
			}
		}
	}
	return http.StatusBadRequest
}
