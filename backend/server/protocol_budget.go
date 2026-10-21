package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func (s *Server) pinProtocolBudget(ctx context.Context) (context.Context, error) {
	limit := s.config.GetMaxBodyBytes()
	if limit <= 0 {
		limit = protocol.DefaultRequestBodyBytes
	}
	return protocol.WithRequestBodyLimit(ctx, limit)
}

func (s *Server) pinProtocolRequest(ctx context.Context, c *app.RequestContext) (context.Context, bool) {
	ctx, err := s.pinProtocolBudget(ctx)
	if err != nil {
		respondFail(ctx, c, http.StatusInternalServerError, "invalid_resource_config", err.Error())
		return ctx, false
	}
	return ctx, true
}

// Preview envelopes contain semantic snapshots as well as wire input. Their
// envelope cap is separate; the contained definition and input are checked again.
func protocolPreviewPath(path string) bool {
	return strings.HasPrefix(path, "/api/admin/protocols/") && strings.HasSuffix(path, "/preview")
}

func (s *Server) readGatewayRequest(ctx context.Context, c *app.RequestContext, compiled *protocol.Compiled) ([]byte, bool) {
	var budgetOK bool
	ctx, budgetOK = s.pinProtocolRequest(ctx, c)
	if !budgetOK {
		return nil, false
	}
	limit := compiled.RequestLimits(ctx).BufferBytes
	declared := int64(c.Request.Header.ContentLength())
	reservation := int64(limit)
	if declared >= 0 {
		reservation = min(reservation, max(1, declared))
	}
	if err := s.acquireGatewayPreparation(ctx, c, reservation); err != nil {
		s.rejectPreparation(ctx, c, compiled, err)
		return nil, false
	}
	// A declared length is not trusted: read at most one extra byte and reject
	// mismatches rather than extending a reservation while holding other slots.
	// Unknown/chunked input reserves the full pinned request limit up front.
	reader := io.Reader(requestBody(c))
	if declared >= 0 && declared < int64(limit) {
		reader = io.LimitReader(reader, declared+1)
	}
	body, err := protocol.ReadBoundedBody(reader, limit)
	if err == nil && declared >= 0 && int64(len(body)) != declared {
		err = fmt.Errorf("request body length differs from Content-Length")
	}
	if err == nil {
		return body, true
	}
	s.releaseGatewayPreparation(c)
	err = protocol.AnnotateResourceError(err, compiled.Identity(), protocol.DecodeRequest, "read.request")
	record := s.initUsageRecord(ctx, c, time.Now(), nil, builtin.FormatType(compiled.Identity().Family))
	record.IngressRevision, record.SourceFormat = compiled.Hash(), compiled.Identity().DefinitionID
	record.RelayMode = "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	s.failGateway(ctx, c, record, http.StatusBadRequest, err)
	s.recordUsage(record)
	return nil, false
}

// localResourceStatus only classifies pre-send failures; observed HTTP and
// provider failures keep their primary status and cancellation wins upstream.
func localResourceStatus(record *usageRecord, err error) int {
	if record.UpstreamHTTPStatus != 0 || generationFailureClass(err) != "" {
		return 0
	}
	var resource *protocol.ResourceLimitError
	hasResource := errors.As(err, &resource)
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		for _, issue := range conversion.Issues {
			if issue.Stage == "read.request" && hasResource && resource.Metric == "bytes" {
				return http.StatusRequestEntityTooLarge
			}
			if issue.Code == protocol.LimitExceeded && (issue.Direction == protocol.DecodeRequest || issue.Direction == protocol.EncodeRequest) {
				return http.StatusBadRequest
			}
		}
	}
	if hasResource {
		return http.StatusBadRequest
	}
	return 0
}
