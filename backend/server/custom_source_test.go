package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

func registerDiscoveryProtocol(t *testing.T) {
	t.Helper()
	if err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "vendor-discovery",
		Request: relay.CustomProtocolRequest{
			PathTemplate: "/api/v1/generate",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
			Auth:         relay.CustomProtocolAuth{Mode: "bearer"},
		},
		Models: &relay.CustomProtocolModels{
			Path:     "/api/v1/models",
			ListPath: "models",
			NamePath: "display_name",
		},
	}); err != nil {
		t.Fatalf("register discovery protocol: %v", err)
	}
}

func TestValidateCustomSourceProtocol(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	if err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID:      "vendor-json",
		Request: relay.CustomProtocolRequest{BodyTemplate: `{}`},
	}); err != nil {
		t.Fatalf("register custom protocol: %v", err)
	}

	item := storage.ModelSource{Platform: "CUSTOM:Vendor-JSON", AutoFetchModels: false}
	if err := validateCustomSourceProtocol(&item); err != nil {
		t.Fatalf("validate registered custom protocol: %v", err)
	}
	if item.Platform != "custom:vendor-json" {
		t.Fatalf("custom platform was not normalized: %q", item.Platform)
	}

	item = storage.ModelSource{Platform: "custom:missing", AutoFetchModels: false}
	if err := validateCustomSourceProtocol(&item); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected unregistered protocol error, got %v", err)
	}

	// 未声明 models 发现配置的协议仍要求 manualModels。
	item = storage.ModelSource{Platform: "custom:vendor-json", AutoFetchModels: true}
	if err := validateCustomSourceProtocol(&item); err == nil || !strings.Contains(err.Error(), "does not define model discovery") {
		t.Fatalf("expected discovery-missing error, got %v", err)
	}

	// 声明了 models 发现配置的协议允许自动拉取。
	registerDiscoveryProtocol(t)
	item = storage.ModelSource{Platform: "custom:vendor-discovery", AutoFetchModels: true}
	if err := validateCustomSourceProtocol(&item); err != nil {
		t.Fatalf("discovery-enabled protocol must allow autoFetchModels: %v", err)
	}
}

func TestCustomSourceModelDiscoveryRequiresManualModels(t *testing.T) {
	_, err := (&Server{}).fetchModelsFromSource(context.Background(), storage.ModelSource{Platform: "custom:vendor-json"}, "")
	if err == nil || !strings.Contains(err.Error(), "does not define model discovery") {
		t.Fatalf("expected custom discovery error, got %v", err)
	}
}

// 协议声明发现端点后,custom 源按 models.path 拉取并解析模型列表:
// 鉴权继承 request.auth(bearer),listPath/idPath/namePath 各自生效。
func TestFetchCustomProtocolModels(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerDiscoveryProtocol(t)

	var gotAuth, gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"id":"m-a","display_name":"Model A"},{"id":""},{"id":"m-b"}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(nil)
	models, err := s.fetchModelsFromSource(context.Background(), storage.ModelSource{
		Platform: "custom:vendor-discovery",
		BaseURL:  upstream.URL + "/wrong-prefix",
		// 拉取专用地址覆盖 baseUrl:发现端点与转发端点不同面时各自生效。
		FetchBaseURL: upstream.URL,
	}, "sk-test")
	if err != nil {
		t.Fatalf("fetch custom protocol models: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/models" {
		t.Fatalf("discovery request must hit models.path, got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("discovery must inherit request auth, got %q", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models (empty-id entry skipped), got %d: %#v", len(models), models)
	}
	if models[0].ID != "m-a" || models[0].Name != "Model A" {
		t.Fatalf("namePath must map display name, got %#v", models[0])
	}
	if models[1].ID != "m-b" || models[1].Name != "m-b" {
		t.Fatalf("missing namePath value must fall back to id, got %#v", models[1])
	}
	if models[0].Platform != "custom:vendor-discovery" {
		t.Fatalf("model platform must follow the source, got %q", models[0].Platform)
	}
}

// 发现端点非 2xx 时错误必须携带上游状态与原文片段,便于在源列表排查。
func TestFetchCustomProtocolModelsUpstreamError(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerDiscoveryProtocol(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer upstream.Close()

	s := newTestServer(nil)
	_, err := s.fetchModelsFromSource(context.Background(), storage.ModelSource{
		Platform: "custom:vendor-discovery", BaseURL: upstream.URL,
	}, "sk-bad")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected upstream error surfaced, got %v", err)
	}
}
