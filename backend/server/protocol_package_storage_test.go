package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestPackageGatewayStorageDelivery(t *testing.T) {
	for _, name := range []string{"openai-responses", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			packages := seedGatewayPackages(t, server)
			upstream, ingress := packages[name], packages["openai-responses"]
			isResponses := name == "openai-responses"
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				fields, err := mustProtocolValue(t, string(body)).ReadObject()
				if err != nil {
					t.Error(err)
					return
				}
				if isResponses && fields["store"].JSONString() != "false" || !isResponses && !fields["store"].IsZero() {
					t.Error("outgoing instruction differs from the selected storage disposition", string(body))
				}
				isStream := fields["stream"].JSONString() == "true" || strings.Contains(request.URL.Path, "streamGenerateContent")
				reply := buildPackageGatewayReply(t, upstream, isStream)
				if isStream {
					writer.Header().Set("Content-Type", "text/event-stream")
					if isResponses {
						reply = bytes.ReplaceAll(reply, []byte(`"response":{`), []byte(`"response":{"store":true,`))
					}
				} else {
					writer.Header().Set("Content-Type", "application/json")
					if isResponses {
						reply = append([]byte(`{"store":true,`), reply[1:]...)
					}
				}
				_, _ = writer.Write(reply)
			}))
			defer provider.Close()
			setupGatewayModelWithCapabilities(t, server, upstream, provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
			for _, isStream := range []bool{false, true} {
				raw := fmt.Sprintf(`{"model":"group","input":"hi","store":%t,"stream":%t}`, !isResponses, isStream)
				request := httptest.NewRequest(http.MethodPost, "/gateway/"+ingress.Identity().DefinitionID+ingress.Operations()["generate"].Path, strings.NewReader(raw))
				request.Header.Set("Authorization", "Bearer gateway-test-token")
				reply := httptest.NewRecorder()
				before := calls.Load()
				serveTestHTTP(server.engine, reply, request)
				if reply.Code != http.StatusOK || calls.Load() != before+1 {
					t.Fatal("storage conversion failed or repeated generation", reply.Code, calls.Load(), reply.Body.String())
				}
				if isStream {
					seen := 0
					err := protocol.ReadFrames(t.Context(), bytes.NewReader(reply.Body.Bytes()), ingress.Operations()["stream"], ingress.ResourceLimits().BufferBytes, func(value protocol.Value, _ protocol.Object) error {
						fields, err := value.ReadObject()
						if err != nil {
							return err
						}
						if fields["type"].IsString("response.created") || fields["type"].IsString("response.completed") {
							response, err := fields["response"].ReadObject()
							if err != nil || response["store"].JSONString() != "false" {
								return fmt.Errorf("client response does not reflect the effective storage decision: %s", value.JSONString())
							}
							seen++
						}
						return nil
					})
					if err != nil || seen != 2 {
						t.Fatal("missing final delivery envelopes", seen, err)
					}
				} else {
					fields, err := mustProtocolValue(t, reply.Body.String()).ReadObject()
					if err != nil || fields["store"].JSONString() != "false" {
						t.Fatal("client response retained the provider storage decision", reply.Body.String(), err)
					}
				}
				checkPackageGatewayReply(t, collectPackageGatewayReply(t, ingress, reply, isStream), "hi")
			}
			server.stopUsageWriter()
			if records := latestUsageRecords(t, server); len(records) != 2 || records[0].Error != "" || records[1].Error != "" {
				t.Fatal("storage conversion did not settle once per generation", records)
			}
		})
	}
}

func TestPackageCollectionRejectsConflictingWireStorage(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	compiled := seedGatewayPackages(t, server)["openai-responses"]
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	policy := protocol.DefaultConversionPolicy(compiled, compiled)
	policy.Rules = append(policy.Rules, protocol.ConversionRule{ID: "conflicting-store", Enabled: true, Order: 1000, Phase: protocol.ConversionWire, Action: "set", Path: "/store", Value: mustProtocolValue(t, "true")})
	conversion, err := protocol.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	request, err := compiled.BuildAgentRequest(t.Context(), protocol.Request{SchemaVersion: compiled.CoreVersion(), Source: protocol.AgentIdentity(), Model: protocol.StringValue("gpt-5.4"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hi")}}}}}, protocol.AgentPreferences{MaxOutputTokens: 32})
	if err != nil {
		t.Fatal(err)
	}
	request, err = conversion.Request(t.Context(), request, protocol.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: "gpt-5.4"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate := gatewayCandidate{compiled: compiled, conversion: conversion, operationName: "generate", operation: compiled.Operations()["generate"], model: config.ModelRef{ID: "gpt-5.4", Name: "gpt-5.4", BaseURL: provider.URL},
		binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Capabilities(protocol.EncodeRequest), Transports: []protocol.Transport{protocol.HTTPJSON}}}
	_, err = server.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
	var failure *protocol.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != "/store" || calls.Load() != 0 {
		t.Fatal("late wire change bypassed caller intent or reached the provider", err, calls.Load())
	}
}
