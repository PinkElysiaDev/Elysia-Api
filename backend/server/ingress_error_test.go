package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func localErrorPackage(t *testing.T, field string, rejectAuth bool) protocol.ProtocolPackage {
	t.Helper()
	document := sourceStreamPackage(t)
	document.Spec.ID, document.Spec.Family, document.Spec.Version = "local-errors", "local-errors", field
	document.Mapping.Capabilities = protocol.CapabilitySet{}
	delete(document.Mapping.Directions, protocol.DecodeEvent)
	delete(document.Mapping.Directions, protocol.EncodeEvent)
	payload := protocol.ValueSchema{Type: protocol.ObjectType, Required: []string{"category", "message"}, Properties: map[string]protocol.ValueSchema{
		"category": {Type: protocol.StringType}, "message": {Type: protocol.StringType},
	}}
	document.Spec.Documents["client-response"] = protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType, Required: []string{field}, Properties: map[string]protocol.ValueSchema{field: payload}}}
	document.Spec.Documents["provider-response"] = document.Spec.Documents["client-response"]
	decode := document.Mapping.Directions[protocol.DecodeResponse]
	decode.Program.Fields["error"] = protocol.Expression{Op: "read", Path: "/" + field}
	decode.Effects[0].Target = "/" + field
	decode.Effects[0].When.Source.Path = "/" + field
	document.Mapping.Directions[protocol.DecodeResponse] = decode
	mapping := document.Mapping.Directions[protocol.EncodeResponse]
	mapping.Program = protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{field: {Op: "read", Path: "/error"}}}
	mapping.Effects[0].Target = "/" + field
	if rejectAuth {
		when := protocol.Expression{Op: "equal", Items: []protocol.Expression{{Op: "read", Path: "/error/category"}, {Op: "literal", Value: protocol.StringValue("authentication")}}}
		program := mapping.Program
		mapping.Program = protocol.Expression{Op: "if", When: &when, Then: &protocol.Expression{Op: "reject", Path: "/error", Reason: "synthetic unrenderable authentication", Diagnostic: &protocol.RejectDiagnostic{Code: protocol.ConversionRejected, Stage: "wire"}}, Otherwise: &program}
	}
	document.Mapping.Directions[protocol.EncodeResponse] = mapping
	errorValue := mustProtocolValue(t, `{"category":"upstream","message":"fixture failure"}`)
	coreError := mustEncodedProtocolValue(t, map[string]any{"schemaVersion": protocol.ProtocolCoreVersion, "error": errorValue})
	document.Mapping.Samples = []protocol.Sample{
		{ID: "request", Direction: protocol.EncodeRequest, Input: mustProtocolValue(t, `{"schemaVersion":2,"model":"m","content":[]}`), Expected: mustProtocolValue(t, `{"model":"m"}`)},
		{ID: "failure.decode", Direction: protocol.DecodeResponse, Input: mustEncodedProtocolValue(t, protocol.Object{field: errorValue}), Expected: coreError},
		{ID: "failure.encode", Direction: protocol.EncodeResponse, Input: coreError, Expected: mustEncodedProtocolValue(t, protocol.Object{field: errorValue})},
	}
	return document
}

func TestIngressErrorMappingPinsBeforeAuthentication(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	first := localErrorPackage(t, "problem", false)
	original := seedGatewayProtocol(t, server, first.Spec.ID, mustEncodedProtocolValue(t, first).Bytes(), true)
	var updated *protocol.Compiled
	server.engine.Any("/gateway/:protocolId/*path", func(ctx context.Context, c *app.RequestContext) {
		selection := server.pinIngress(c)
		if updated == nil {
			second := localErrorPackage(t, "failure", false)
			updated = seedGatewayProtocol(t, server, second.Spec.ID, mustEncodedProtocolValue(t, second).Bytes(), true)
			pinned, _ := selection.view.Pin(first.Spec.ID)
			if pinned.Hash() != original.Hash() || selection.compiled.Hash() != original.Hash() || updated.Hash() == original.Hash() {
				t.Fatal("activation replaced the in-flight ingress snapshot")
			}
		}
		c.Next(ctx)
	}, server.authMiddleware(), server.gatewayProtocol)
	for _, field := range []string{"problem", "failure"} {
		request := httptest.NewRequest(http.MethodPost, "/gateway/local-errors/generate", strings.NewReader("not JSON"))
		reply := httptest.NewRecorder()
		serveTestHTTP(server.engine, reply, request)
		if reply.Code != http.StatusUnauthorized || !strings.Contains(reply.Body.String(), `"`+field+`":{"category":"authentication"`) || strings.Contains(reply.Body.String(), "protocol_gateway_error") {
			t.Fatal("configured early failure changed revision or read invalid body", reply.Code, reply.Body.String())
		}
	}
	// The same registered custom endpoint changes only its Mapping. Rendering
	// failure keeps the primary status and uses the fixed gateway envelope.
	broken := localErrorPackage(t, "failure", true)
	seedGatewayProtocol(t, server, broken.Spec.ID, mustEncodedProtocolValue(t, broken).Bytes(), true)
	for _, path := range []string{"/gateway/local-errors/generate", "/gateway/missing/generate"} {
		reply := httptest.NewRecorder()
		serveTestHTTP(server.engine, reply, httptest.NewRequest(http.MethodPost, path, strings.NewReader("not JSON")))
		if reply.Code != http.StatusUnauthorized || !strings.Contains(reply.Body.String(), `"code":"protocol_gateway_error"`) || strings.Contains(reply.Body.String(), "synthetic unrenderable") {
			t.Fatal("failed selection or mapping replaced the primary error", reply.Code, reply.Body.String())
		}
	}
}

func TestDefaultPackageIngressErrors(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, server)
	server.config.MaxBodyBytes = 1024
	if err := server.store.UpsertAPIToken(t.Context(), storage.APIToken{Name: "ingress-fixture", Token: "ingress-test-token", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	server.setupRoutes()
	for _, fixture := range []struct{ name, path, category string }{
		{"openai-chat", "/v1/chat/completions", `"code":"invalid_api_key"`},
		{"openai-responses", "/v1/responses", `"code":"invalid_api_key"`},
		{"anthropic", "/v1/messages", `"type":"authentication_error"`},
		{"google-generate-content", "/v1beta/models/m:generateContent", `"status":"UNAUTHENTICATED"`},
	} {
		compiled := packages[fixture.name]
		for _, path := range []string{fixture.path, "/gateway/" + compiled.Identity().DefinitionID + fixture.path} {
			reply := httptest.NewRecorder()
			serveTestHTTP(server.engine, reply, httptest.NewRequest(http.MethodPost, path, strings.NewReader("not JSON")))
			if reply.Code != http.StatusUnauthorized || !strings.Contains(reply.Body.String(), fixture.category) {
				t.Fatal("endpoint did not use its declared error mapping", path, reply.Code, reply.Body.String())
			}
			value := mustProtocolValue(t, reply.Body.String())
			if err := compiled.ValidateResponseOutput(t.Context(), value, value, protocol.EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(buildPackageGatewayRequest(fixture.name, false)))
		request.Header.Set("Authorization", "Bearer ingress-test-token")
		reply := httptest.NewRecorder()
		serveTestHTTP(server.engine, reply, request)
		if reply.Code != http.StatusNotFound || strings.Contains(reply.Body.String(), "protocol_gateway_error") {
			t.Fatal("authorized local failure bypassed the selected mapping", fixture.path, reply.Code, reply.Body.String())
		}
		value := mustProtocolValue(t, reply.Body.String())
		if err := compiled.ValidateResponseOutput(t.Context(), value, value, protocol.EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 2048)))
	request.Header.Set("Authorization", "Bearer ingress-test-token")
	reply := httptest.NewRecorder()
	serveTestHTTP(server.engine, reply, request)
	if reply.Code != http.StatusRequestEntityTooLarge || !strings.Contains(reply.Body.String(), `"type":"invalid_request_error"`) || strings.Contains(reply.Body.String(), "protocol_gateway_error") {
		t.Fatal("resource rejection bypassed the selected mapping", reply.Code, reply.Body.String())
	}
}
