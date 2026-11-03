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

func seedClientCatalogGroups(t *testing.T, server *Server) {
	t.Helper()
	for _, group := range []storage.ModelGroup{
		{ID: "catalog-default", Name: "local-default", Enabled: true, Strategy: "sequential"},
		{ID: "catalog-limit", Name: "local-limit", Enabled: true, MaxTokens: 2048, Strategy: "sequential"},
		{ID: "catalog-disabled", Name: "hidden", Strategy: "sequential"},
	} {
		if err := server.store.UpsertGroup(t.Context(), group); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.store.UpsertAPIToken(t.Context(), storage.APIToken{Name: "catalog", Token: "catalog-test-token", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	server.invalidateRouteCache()
}

func clientCatalogRequest(t *testing.T, server *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer catalog-test-token")
	reply := httptest.NewRecorder()
	serveTestHTTP(server.engine, reply, request)
	return reply
}

func TestPackageClientCatalogPublicAndGateway(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, server)
	seedClientCatalogGroups(t, server)
	server.setupRoutes()
	for _, fixture := range []struct{ name, path string }{
		{"openai-chat", "/v1/models"}, {"google-generate-content", "/v1beta/models"},
	} {
		compiled := packages[fixture.name]
		for _, path := range []string{fixture.path, "/gateway/" + compiled.Identity().DefinitionID + fixture.path} {
			reply := clientCatalogRequest(t, server, path)
			if reply.Code != http.StatusOK || strings.Contains(reply.Body.String(), "hidden") {
				t.Fatal(path, reply.Code, reply.Body.String())
			}
			page, err := compiled.DecodeModelPage(t.Context(), "models", mustProtocolValue(t, reply.Body.String()))
			if err != nil || len(page.Models) != 2 || page.Models[0].ID != "local-default" || page.Models[1].ID != "local-limit" {
				t.Fatal("local catalog changed membership", path, page, err)
			}
			if fixture.name == "google-generate-content" && (!strings.Contains(reply.Body.String(), `"inputTokenLimit":1048576`) || !strings.Contains(reply.Body.String(), `"inputTokenLimit":2048`) || !strings.Contains(reply.Body.String(), `"outputTokenLimit":8192`)) {
				t.Fatal("declared token limits were not encoded", reply.Body.String())
			}
		}
	}
}

func localCatalogPackage(t *testing.T, field string) protocol.ProtocolPackage {
	t.Helper()
	document := localErrorPackage(t, "problem", false)
	document.Spec.ID, document.Spec.Family, document.Spec.Version = "local-catalog", "local-catalog", field
	document.Spec.Documents["catalog-source"] = protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType, AllowUnknown: true}}
	document.Spec.Documents["catalog-client"] = protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType, Required: []string{field}, Properties: map[string]protocol.ValueSchema{
		field: {Type: protocol.ArrayType, Items: &protocol.ValueSchema{Type: protocol.StringType}},
	}}}
	document.Spec.Operations["models"] = protocol.ProtocolOperationSpec{Kind: "models", Method: http.MethodGet, Path: "/catalog", Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}, Response: "catalog-source", Models: &protocol.ProtocolModelDiscoverySpec{ClientDocument: "catalog-client"}}
	encoder := protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{field: {Op: "map", Source: &protocol.Expression{Op: "read", Path: "/models"}, Body: &protocol.Expression{Op: "read", From: "item", Path: "/id"}}}}
	document.Mapping.Operations["models"] = protocol.ProtocolOperationMapping{DecodeModels: &protocol.Expression{Op: "read"}, EncodeModels: &encoder}
	document.Mapping.ModelSamples = []protocol.ModelSample{
		{ID: "catalog.decode", Operation: "models", Input: mustProtocolValue(t, `{"models":[]}`), Expected: mustProtocolValue(t, `{"models":[]}`)},
		{ID: "catalog.encode", Operation: "models", Direction: protocol.EncodeResponse, Input: mustProtocolValue(t, `{"models":[{"id":"example"}]}`), Expected: mustEncodedProtocolValue(t, map[string]any{field: []string{"example"}})},
	}
	return document
}

func TestPackageClientCatalogPinsConfiguredShape(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	seedClientCatalogGroups(t, server)
	first := localCatalogPackage(t, "available")
	original := seedGatewayProtocol(t, server, first.Spec.ID, mustEncodedProtocolValue(t, first).Bytes(), true)
	var updated *protocol.Compiled
	server.engine.Any("/gateway/:protocolId/*path", func(ctx context.Context, c *app.RequestContext) {
		selection := server.pinIngress(c)
		if updated == nil {
			second := localCatalogPackage(t, "choices")
			updated = seedGatewayProtocol(t, server, second.Spec.ID, mustEncodedProtocolValue(t, second).Bytes(), true)
			if selection.compiled.Hash() != original.Hash() || updated.Hash() == original.Hash() {
				t.Fatal("catalog activation replaced the pinned snapshot")
			}
		}
		c.Next(ctx)
	}, server.authMiddleware(), server.gatewayProtocol)
	for _, field := range []string{"available", "choices"} {
		reply := clientCatalogRequest(t, server, "/gateway/local-catalog/catalog")
		want := `{"` + field + `":["local-default","local-limit"]}`
		if reply.Code != http.StatusOK || reply.Body.String() != want {
			t.Fatal("catalog did not use pinned Spec and Mapping", reply.Code, reply.Body.String(), want)
		}
	}
	missing := localCatalogPackage(t, "missing")
	missing.Spec.Operations["models"].Models.ClientDocument = ""
	binding := missing.Mapping.Operations["models"]
	binding.EncodeModels = nil
	missing.Mapping.Operations["models"] = binding
	missing.Mapping.ModelSamples = missing.Mapping.ModelSamples[:1]
	seedGatewayProtocol(t, server, missing.Spec.ID, mustEncodedProtocolValue(t, missing).Bytes(), true)
	reply := clientCatalogRequest(t, server, "/gateway/local-catalog/catalog")
	if reply.Code != http.StatusNotFound || strings.Contains(reply.Body.String(), `"object":"list"`) || !strings.Contains(reply.Body.String(), `"problem"`) {
		t.Fatal("missing catalog encoder selected an implicit format", reply.Code, reply.Body.String())
	}
}
