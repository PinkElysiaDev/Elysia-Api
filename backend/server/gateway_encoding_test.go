package server

import (
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestGatewayOutboundEncodingOwnsActualInputs(t *testing.T) {
	compiled := conversionPackage(t, protocol.PresetAnthropicID)
	for _, hasPrepared := range []bool{false, true} {
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"alias","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`), protocol.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		request.ClientOutput = &protocol.ClientOutput{CollectUsage: true}
		candidate := gatewayCandidate{compiled: compiled, model: config.ModelRef{ID: "api-model", Name: "display name"}, scope: protocol.Scope{Account: "account"}, operation: protocol.Operation{IsInterleavedThinking: true}}
		if hasPrepared {
			candidate.prepared = request
		}
		record := &usageRecord{RequestID: "outbound-execution", StartedAt: time.Unix(123, 0)}
		encoding, err := candidate.prepareRequestEncoding(request, record)
		if err != nil {
			t.Fatal(err)
		}
		if encoding.request == request || !encoding.request.Model.IsString("api-model") || !request.Model.IsString("alias") || encoding.options.Scope != candidate.scope || !encoding.options.InterleavedThinking || encoding.options.ClientOutput != encoding.request.ClientOutput {
			t.Fatal("outbound inputs differ from the authorized attempt")
		}
		encoding.value, err = compiled.EncodeRequestValue(t.Context(), encoding.request, encoding.options)
		if err != nil {
			t.Fatal(err)
		}
		fields, err := encoding.value.ReadObject()
		if err != nil || !fields["model"].IsString("api-model") || fields["messages"].IsZero() {
			t.Fatal("incorrect outbound body", encoding.value, err)
		}
		warning := protocol.ConversionIssue{Severity: protocol.SeverityWarning, Reason: "preflight diagnostic"}
		encoding.options.Diagnostics.Add(warning)
		candidate.encoding = encoding
		actual, err := candidate.prepareRequestEncoding(request, record)
		if err != nil || actual != encoding || actual.value != encoding.value || len(actual.options.Diagnostics.Issues()) != 1 {
			t.Fatal("dispatch rebuilt outbound inputs or lost diagnostics", err)
		}
		body := encoding.value
		candidate.releaseRequestEncoding()
		if !encoding.value.IsZero() || encoding.request != nil {
			t.Fatal("release retained the outbound body")
		}
		retry, err := candidate.prepareRequestEncoding(request, record)
		if err != nil || retry == encoding || !retry.value.IsZero() || len(retry.options.Diagnostics.Issues()) != 0 {
			t.Fatal("retry reused released state", err)
		}
		value, err := compiled.EncodeRequestValue(t.Context(), retry.request, retry.options)
		if err != nil || value != body {
			t.Fatal("retry changed outbound body", value, err)
		}
	}
}
