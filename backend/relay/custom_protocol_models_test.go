package relay

import (
	"strings"
	"testing"
)

func modelsProtocol(mutate func(*CustomProtocolModels)) CustomProtocolConfig {
	models := &CustomProtocolModels{Path: "/v1/models", ListPath: "data"}
	if mutate != nil {
		mutate(models)
	}
	return CustomProtocolConfig{
		ID:       "discovery-vendor",
		Request:  CustomProtocolRequest{Method: "POST", PathTemplate: "/v1/chat", BodyTemplate: `{"model":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{TextPath: "text"},
		Models:   models,
	}
}

func TestValidateCustomProtocolModels(t *testing.T) {
	if err := ValidateCustomProtocol(modelsProtocol(nil)); err != nil {
		t.Fatalf("valid models discovery must pass: %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.Path = "" })); err == nil || !strings.Contains(err.Error(), "models.path is required") {
		t.Fatalf("missing path must fail, got %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.Method = "DELETE" })); err == nil || !strings.Contains(err.Error(), "models method") {
		t.Fatalf("non-GET/POST method must fail, got %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.ListPath = "" })); err == nil || !strings.Contains(err.Error(), "models.listPath is required") {
		t.Fatalf("missing listPath must fail, got %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.ListPath = "bad[path" })); err == nil || !strings.Contains(err.Error(), "models.listPath") {
		t.Fatalf("bad listPath must fail, got %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.Headers = map[string]string{"Authorization": "x"} })); err == nil || !strings.Contains(err.Error(), "managed by the relay") {
		t.Fatalf("protected header must fail, got %v", err)
	}
	if err := ValidateCustomProtocol(modelsProtocol(func(m *CustomProtocolModels) { m.Auth = &CustomProtocolAuth{Mode: "bogus"} })); err == nil || !strings.Contains(err.Error(), "models.auth") {
		t.Fatalf("bad models auth must fail, got %v", err)
	}
}

func TestRenderCustomProtocolModelsRequest(t *testing.T) {
	config := modelsProtocol(func(m *CustomProtocolModels) {
		m.Headers = map[string]string{"X-Vendor": "lists"}
		m.Query = map[string]string{"limit": "100"}
		m.Auth = &CustomProtocolAuth{Mode: "header", Header: "x-api-key"}
	})
	rendered, err := RenderCustomProtocolModelsRequest(config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.Method != "GET" || rendered.Path != "/v1/models" {
		t.Fatalf("unexpected method/path: %s %s", rendered.Method, rendered.Path)
	}
	if rendered.Headers["X-Vendor"] != "lists" || rendered.Query["limit"] != "100" {
		t.Fatalf("headers/query must pass through: %#v", rendered)
	}
	if rendered.Auth.Mode != "header" || rendered.Auth.Header != "x-api-key" {
		t.Fatalf("models.auth must override request auth: %#v", rendered.Auth)
	}

	// 缺省:method=GET,鉴权继承 request.auth。
	config.Request.Auth = CustomProtocolAuth{Mode: "query", Query: "apikey"}
	config.Models.Auth = nil
	config.Models.Method = "POST"
	rendered, err = RenderCustomProtocolModelsRequest(config)
	if err != nil {
		t.Fatalf("render 2: %v", err)
	}
	if rendered.Method != "POST" || rendered.Auth.Mode != "query" || rendered.Auth.Query != "apikey" {
		t.Fatalf("defaults must apply: %#v", rendered)
	}

	if _, err := RenderCustomProtocolModelsRequest(CustomProtocolConfig{ID: "x", Request: config.Request}); err == nil || !strings.Contains(err.Error(), "does not define model discovery") {
		t.Fatalf("missing models config must fail, got %v", err)
	}
}

func TestParseCustomProtocolModels(t *testing.T) {
	config := modelsProtocol(func(m *CustomProtocolModels) {
		m.ListPath = "output.models"
		m.IDPath = "model_id"
		m.NamePath = "friendly"
	})
	infos, err := ParseCustomProtocolModels([]byte(`{"output":{"models":[
		{"model_id":"a","friendly":"Alpha"},
		{"model_id":"b"},
		{"friendly":"no-id"},
		{"model_id":""}
	]}}`), config)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(infos) != 2 || infos[0].ID != "a" || infos[0].Name != "Alpha" || infos[1].ID != "b" || infos[1].Name != "b" {
		t.Fatalf("unexpected parse result: %#v", infos)
	}

	if _, err := ParseCustomProtocolModels([]byte(`{"data":[]}`), config); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing list path must fail, got %v", err)
	}
	if _, err := ParseCustomProtocolModels([]byte(`{"output":{"models":{"a":1}}}`), config); err == nil || !strings.Contains(err.Error(), "does not point to an array") {
		t.Fatalf("non-array list path must fail, got %v", err)
	}
}
