package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const gatewayExecutionLedger = `[{"type":"message","input_tokens":10,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},{"type":"advisor_message","model":"advisor","input_tokens":9007199254740993,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`

func TestGatewayExecutionUsageSurvivesClientProjection(t *testing.T) {
	for _, clientName := range []string{"openai-chat", "openai-responses", "google-generate-content"} {
		for _, isStream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", clientName, isStream), func(t *testing.T) {
				checkGatewayExecutionUsage(t, clientName, isStream)
			})
		}
	}
}

func checkGatewayExecutionUsage(t *testing.T, clientName string, isStream bool) {
	t.Helper()
	server, _ := newProtocolAdminTestServer(t)
	bodyLimit, errorsOnly := 1, true
	server.config.SetUsageLogConfig(config.UsageLogConfig{BodyMaxKB: &bodyLimit, BodyOnErrorOnly: &errorsOnly})
	packages := defaultGatewayPackages(t, server)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if !isStream {
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"id":"r","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"speed":"fast","iterations":`+gatewayExecutionLedger+`}}`)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"r","type":"message","model":"upstream-model","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0,"speed":"fast"}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(writer, "data: %s\n\n", frame)
		}
	}))
	defer provider.Close()
	setupGatewayModelWithCapabilities(t, server, packages["anthropic"], provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
	client := packages[clientName]
	operation := "generate"
	if isStream {
		operation = "stream"
	}
	path := strings.ReplaceAll(client.Operations()[operation].Path, "{model}", "group")
	request := httptest.NewRequest(http.MethodPost, "/gateway/"+client.Identity().DefinitionID+path, strings.NewReader(buildPackageGatewayRequest(clientName, isStream)))
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	request.Header.Set("Content-Type", "application/json")
	reply := httptest.NewRecorder()
	serveTestHTTP(server.engine, reply, request)
	if reply.Code != http.StatusOK || strings.Contains(reply.Body.String(), `"speed"`) || strings.Contains(reply.Body.String(), `"iterations"`) {
		t.Fatal("execution disclosure projection failed", reply.Code, reply.Body.String())
	}
	response := collectPackageGatewayReply(t, client, reply, isStream, protocol.Scope{})
	checkPackageGatewayReply(t, response, "OK", 10, 2)
	records := latestUsageRecords(t, server)
	if len(records) != 1 || records[0].InputTokens != 10 || records[0].OutputTokens != 2 || records[0].TotalTokens != 12 {
		t.Fatal("execution ledger changed legacy summary totals", records)
	}
	raw := storedRecordJSON(t, server.store, records[0].RequestID)
	var saved usageRecord
	if err := sonic.ConfigStd.UnmarshalFromString(raw, &saved); err != nil {
		t.Fatal(err)
	}
	usage := saved.ProtocolUsage
	if usage == nil || !usage.ExecutionSpeed.IsString("fast") || usage.Input.Count != 10 || usage.Output.Count != 2 || usage.Total.Count != 12 {
		t.Fatal("source execution facts lost before persistence", usage)
	}
	if isStream {
		if !usage.Executions.IsZero() {
			t.Fatal("stream without iterations fabricated a ledger", usage.Executions)
		}
	} else if !strings.Contains(usage.Executions.JSONString(), "9007199254740993") || !strings.Contains(usage.Executions.JSONString(), `"topLevelRelation":"excluded"`) {
		t.Fatal("persisted execution ledger lost precision or inclusion facts", usage.Executions)
	}
	if saved.IncomingBody.Content != "" || saved.ProviderResponse.Content != "" || saved.DownstreamResponse.Content != "" || saved.OutgoingBody.Content != "" {
		t.Fatal("execution persistence depended on retaining response bodies")
	}
	hasDisclosure := false
	for _, issue := range saved.ConversionIssues {
		if strings.Contains(issue.RuleID, "-execution-speed-disclosure") && issue.Fidelity == "lossy_compatible" && issue.PolicyHash != "" && issue.PolicyRevision != "" {
			hasDisclosure = true
		}
	}
	if !hasDisclosure {
		t.Fatal("source record omitted the client disclosure decision", saved.ConversionIssues)
	}
}

func TestExecutionUsageQueueOwnershipAndBudget(t *testing.T) {
	ledger, err := protocol.ParseValue([]byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{ProtocolUsage: &protocol.Usage{
		ExecutionSpeed: protocol.StringValue("fast"), Executions: ledger,
		Input: &protocol.Counter{Count: 10, Origin: protocol.ObservedCount}, Output: &protocol.Counter{Count: 2, Origin: protocol.ObservedCount},
		Details: map[string]protocol.Counter{"input.text_tokens": {Count: 10, Origin: protocol.ObservedCount}},
	}}
	size := usageRetainedPayloadBytes(record)
	if size <= int64(ledger.Len()+record.ProtocolUsage.ExecutionSpeed.Len()) {
		t.Fatal("queue budget omitted retained usage structure", size)
	}
	writer := &usageWriterState{queue: make(chan *usageRecord, 1), retainedBytes: usageQueuePayloadBytes - size + 1}
	server := &Server{usageWriter: writer}
	if server.enqueueUsageRecord(record) {
		t.Fatal("usage ledger bypassed the cumulative queue budget")
	}
	writer.retainedBytes--
	if !server.enqueueUsageRecord(record) {
		t.Fatal("exact usage budget boundary was rejected")
	}
	record.ProtocolUsage.Input.Count = 99
	record.ProtocolUsage.Details["input.text_tokens"] = protocol.Counter{Count: 99, Origin: protocol.ObservedCount}
	record.ProtocolUsage.ExecutionSpeed = protocol.StringValue("standard")
	record.ProtocolUsage.Executions = protocol.Value{}
	queued := <-writer.queue
	if queued.ProtocolUsage.Input.Count != 10 || queued.ProtocolUsage.Total != nil || queued.ProtocolUsage.Details["input.text_tokens"].Count != 10 || !queued.ProtocolUsage.ExecutionSpeed.IsString("fast") || queued.ProtocolUsage.Executions != ledger {
		t.Fatal("queued usage was mutated or inferred a missing total", queued.ProtocolUsage)
	}
	if writer.retainedBytes != usageQueuePayloadBytes {
		t.Fatal("in-flight execution usage reservation was released early")
	}
	writer.queue <- queued
	writer.mu.Lock()
	server.drainUsageQueueFrom(writer)
	writer.mu.Unlock()
	if writer.retainedBytes != usageQueuePayloadBytes-size {
		t.Fatal("draining did not release execution usage retention")
	}
}

func TestExecutionUsagePersistenceRespectsDisableAndRequestIdentity(t *testing.T) {
	server, settings, store := newPersistTestServer(t)
	record := &usageRecord{RequestID: "execution-accounting", StartedAt: time.Now(), EndedAt: time.Now(), StatusCode: http.StatusOK}
	updateRecordProtocolUsage(record, &protocol.Usage{ExecutionSpeed: protocol.StringValue("fast"), Input: &protocol.Counter{Count: 10, Origin: protocol.ObservedCount}, Output: &protocol.Counter{Count: 2, Origin: protocol.ObservedCount}})
	for range 2 {
		if err := server.saveUsageRecordToStore(record); err != nil {
			t.Fatal(err)
		}
	}
	_, records, err := store.QueryUsageLogs(t.Context(), storage.UsageQuery{Limit: 10})
	if err != nil || len(records) != 1 || records[0].TotalTokens != 12 {
		t.Fatal("repeated persistence doubled usage", records, err)
	}
	isEnabled := false
	settings.SetUsageLogConfig(config.UsageLogConfig{PersistEnabled: &isEnabled})
	record.RequestID = "disabled-execution-accounting"
	server.recordUsage(record)
	if _, hasRecord, err := store.GetUsageRecordJSON(t.Context(), record.RequestID); err != nil || hasRecord {
		t.Fatal("execution facts bypassed disabled persistence", hasRecord, err)
	}
}

func TestExecutionUsageSourceCancellationKeepsObservedSpeed(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := defaultGatewayPackages(t, server)
	upstream, client := packages["anthropic"], packages["openai-chat"]
	conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(client, upstream))
	if err != nil {
		t.Fatal(err)
	}
	options, err := (protocol.EvaluationContext{State: protocol.NewEvaluationState()}).WithOperation("execution-cancel", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer options.State.Release()
	route := protocol.ConversionContext{Source: upstream.Identity(), Target: client.Identity(), Transport: protocol.SSE}.WithOperation(options)
	candidate := gatewayCandidate{compiled: upstream, conversion: conversion, binding: protocol.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	record := &usageRecord{}
	source, err := newProtocolSourceStream(ctx, candidate, route, options, record, func(frame *protocol.EventFrame, _ protocol.Object) error {
		before, err := protocol.EncodeValue(record.ProtocolUsage)
		if err != nil {
			return err
		}
		if _, err := conversion.WithFrameUsage(route, frame.Events); err != nil {
			return err
		}
		after, err := protocol.EncodeValue(record.ProtocolUsage)
		if err != nil || before != after {
			t.Error("numeric frame context changed complete source usage", err)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	start, err := protocol.ParseValue([]byte(`{"type":"message_start","message":{"id":"r","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0,"speed":"fast"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Push(start, nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := source.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("source cancellation lost its classification", err)
	}
	usage := record.ProtocolUsage
	if usage == nil || !usage.ExecutionSpeed.IsString("fast") || usage.Input.Count != 10 || usage.Output.Count != 0 || !usage.Executions.IsZero() {
		t.Fatal("cancellation erased facts or invented unobserved execution usage", usage)
	}
}
