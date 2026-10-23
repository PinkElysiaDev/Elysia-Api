package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestResourceFailureEvidenceBoundaries(t *testing.T) {
	resource := &protocol.ResourceLimitError{Domain: "semantic", Metric: "nodes", Limit: 1600000, Observed: 1600001, Path: "/"}
	local := protocol.AnnotateResourceError(resource, protocol.Identity{DefinitionID: "test"}, protocol.DecodeRequest, "conversion.request")
	record := &usageRecord{StatusCode: 200}
	setUsageError(record, t.Context(), local)
	if record.StatusCode != 400 || record.ErrorKind != "limit_exceeded" || len(record.ConversionIssues) != 1 || canRetryGeneration(local) {
		t.Fatal("local failure misclassified", record)
	}
	response := protocol.AnnotateResourceError(resource, protocol.Identity{DefinitionID: "test"}, protocol.DecodeResponse, "read.response")
	record = &usageRecord{StatusCode: 200, UpstreamHTTPStatus: 200}
	setUsageError(record, t.Context(), response)
	if record.StatusCode != 502 || record.ConversionIssues[0].Direction != protocol.DecodeResponse {
		t.Fatal("response failure attributed to client", record)
	}
	primary := &upstreamHTTPFailure{&gatewayFailure{524, errors.Join(protocol.CheckGenerationOutcome(fallbackHTTPFailure(524, nil)), response)}}
	record = &usageRecord{StatusCode: 524, UpstreamHTTPStatus: 524}
	setUsageError(record, t.Context(), primary)
	if record.StatusCode != 524 || record.ErrorKind == "limit_exceeded" || len(record.ConversionIssues) != 1 {
		t.Fatal("HTTP primary lost", record)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	record = &usageRecord{StatusCode: http.StatusOK}
	setUsageError(record, ctx, errors.Join(context.Canceled, local))
	if record.StatusCode != 400 || record.ErrorKind != "limit_exceeded" {
		t.Fatal("later cancellation replaced an independent resource failure", record)
	}
}
