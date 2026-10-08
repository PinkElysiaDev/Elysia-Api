package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestModelDiscoveryEditorAgentAndForwardingShareDefinition(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateCustomDiscovery(t, s)
	service, _ := s.protocolService()
	compiled, _ := service.Pin("vendor-discovery")
	definition, _ := protocol.EncodeValue(compiled.Definition())
	input, _ := protocol.ParseValue([]byte(`{"catalog":[{"key":"custom-id","label":"Custom"}]}`))
	preview := service.PreviewWorkflow(t.Context(), protocol.PreviewInput{Definition: definition, Mode: "models", Operation: "models", Input: input})
	if err := protocol.IssuesError(preview.Issues); err != nil {
		t.Fatal(err)
	}
	tctx := &protocolAuthorContext{draft: definition.Bytes()}
	cliPreview := s.runCLIWithOptions(t.Context(), tctx, "elysia protocol preview --mode models --operation models --sample '"+string(input.Bytes())+"'", true)
	if !cliPreview.OK {
		t.Fatal(cliPreview.Summary, string(cliPreview.MarshalData()))
	}
	var actual protocol.PreviewResult
	decodeDiscoveryCLIResult(t, cliPreview, &actual)
	if actual.Output != preview.Output {
		t.Fatal("editor and Agent preview diverged", actual, preview)
	}
	shouldFail := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Error(r.URL.Path)
		}
		if shouldFail {
			_, _ = w.Write([]byte(`{"error":"not a catalog"}`))
			return
		}
		_, _ = w.Write(input.Bytes())
	}))
	defer provider.Close()
	for _, isFailure := range []bool{false, true} {
		shouldFail = isFailure
		result := s.runCLIWithOptions(t.Context(), tctx, "elysia protocol models --base-url "+provider.URL, true)
		if result.OK == isFailure {
			t.Fatal(result.Summary, string(result.MarshalData()))
		}
		var probe protocolProbeResult
		decodeDiscoveryCLIResult(t, result, &probe)
		if probe.Report.Kind != protocol.UpstreamVerification || (probe.Report.Checks[0].SampleID != "models") {
			t.Fatal("discovery mislabeled as generation or offline evidence", probe)
		}
		if !isFailure && (len(probe.Models) != 1 || probe.Models[0].ID != "custom-id") {
			t.Fatal(probe)
		}
	}
	report, err := s.store.ReadProtocolReport(t.Context(), compiled.Identity().DefinitionID, compiled.Hash())
	if err != nil || !report.Passed || report.Kind != protocol.OfflineVerification {
		t.Fatal("target probe replaced offline evidence", report, err)
	}
}

func decodeDiscoveryCLIResult(t *testing.T, result CLIResult, target any) {
	t.Helper()
	var envelope struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result.MarshalData(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(envelope.Output, "\n") {
		if strings.HasPrefix(line, "{") {
			if err := json.Unmarshal([]byte(line), target); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing CLI JSON result", envelope.Output)
}

func activateCustomDiscovery(t *testing.T, s *Server) {
	t.Helper()
	definition := loadGatewayDefinition(t, "text-alpha")
	definition.ID = "vendor-discovery"
	raw := `{"kind":"models","method":"GET","path":"/api/v1/models","transport":"http_json","auth":{"location":"header","name":"Authorization","prefix":"Bearer "},"models":{"cursorParameter":"cursor","decode":{"transform":{"op":"object","fields":{"models":{"op":"map","source":{"op":"read","path":"/catalog","required":true},"body":{"op":"object","fields":{"id":{"op":"read","from":"item","path":"/key","required":true},"name":{"op":"read","from":"item","path":"/label"}}}},"next":{"op":"read","path":"/next"}}}}}}`
	value, err := protocol.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var operation protocol.Operation
	if err := value.Decode(&operation); err != nil {
		t.Fatal(err)
	}
	definition.Operations["models"] = operation
	for _, sample := range []struct{ id, input, expected string }{
		{"terminal", `{"catalog":[]}`, `{"models":[]}`},
		{"page", `{"catalog":[{"key":"m-a","label":"Model A"}],"next":"p2"}`, `{"models":[{"id":"m-a","name":"Model A"}],"next":"p2"}`},
	} {
		input, _ := protocol.ParseValue([]byte(sample.input))
		expected, _ := protocol.ParseValue([]byte(sample.expected))
		definition.ModelSamples = append(definition.ModelSamples, protocol.ModelSample{ID: sample.id, Operation: "models", Input: input, Expected: expected})
	}
	activateGatewayDefinition(t, s, definition)
}

func TestValidateSourceProtocolRequiresActiveDiscovery(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	item := storage.ModelSource{Platform: "custom:text-alpha"}
	if err := s.validateSourceProtocol(&item); err != nil {
		t.Fatal(err)
	}
	item.AutoFetchModels = true
	if err := s.validateSourceProtocol(&item); err == nil || !strings.Contains(err.Error(), "does not define model discovery") {
		t.Fatal(err)
	}
	item.Platform = "custom:missing"
	if err := s.validateSourceProtocol(&item); err == nil {
		t.Fatal("inactive protocol accepted")
	}
	activateCustomDiscovery(t, s)
	item.Platform = "CUSTOM:vendor-discovery"
	if err := s.validateSourceProtocol(&item); err != nil || item.Platform != "custom:vendor-discovery" {
		t.Fatal(item, err)
	}
}

func TestSourceSaveKeepsIndependentModelProtocolOverride(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	alpha := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	beta := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-beta"))
	source := storage.ModelSource{ID: "source", Name: "Original", Platform: "custom:" + alpha.Identity().DefinitionID, BaseURL: "https://example.invalid", Enabled: true}
	if err := s.saveSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	override := makeProtocolBinding(upgradeBindingKey{kind: "model", source: source.ID, model: "independent"}, beta)
	if err := s.store.SaveProtocolBinding(t.Context(), override); err != nil {
		t.Fatal(err)
	}
	source.Name = "Edited metadata"
	if err := s.saveSource(t.Context(), source); err != nil {
		t.Fatal("valid model override blocked a source metadata edit", err)
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 2 {
		t.Fatal(bindings, err)
	}
	for _, binding := range bindings {
		if binding.Kind == "model" && (binding.ModelID != "independent" || binding.Binding.RevisionHash != beta.Hash()) {
			t.Fatal("source edit overwrote independent model binding", binding)
		}
	}
}

func TestCustomModelDiscoveryPaginationAndFetchBase(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateCustomDiscovery(t, s)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/models" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected discovery request: %s %s", r.Method, r.URL)
		}
		switch calls {
		case 1:
			if r.URL.Query().Get("cursor") != "" {
				t.Error("initial cursor")
			}
			_, _ = w.Write([]byte(`{"catalog":[{"key":"m-a","label":"Model A"}],"next":"p2"}`))
		case 2:
			if r.URL.Query().Get("cursor") != "p2" {
				t.Error("missing cursor")
			}
			_, _ = w.Write([]byte(`{"catalog":[{"key":"m-b"}]}`))
		default:
			t.Error("extra request")
		}
	}))
	defer upstream.Close()
	models, err := s.fetchModelsFromSource(t.Context(), storage.ModelSource{Platform: "custom:vendor-discovery", BaseURL: "https://unused.invalid", FetchBaseURL: upstream.URL}, "secret")
	if err != nil || calls != 2 || len(models) != 2 {
		t.Fatal(models, calls, err)
	}
	if models[0].ID != "m-a" || models[0].Name != "m-a" || models[1].Name != "m-b" || models[0].Platform != "custom:vendor-discovery" {
		t.Fatal(models)
	}
}

func TestCustomModelDiscoveryRejectsMalformedAndUnboundedPages(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateCustomDiscovery(t, s)
	for _, body := range []string{
		`{"catalog":[{"key":""}]}`, `{"catalog":[{"key":"a"},{"key":"a"}]}`, `{"catalog":[],"next":"repeat"}`, `{"error":"not a model list"}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(body)) }))
			defer upstream.Close()
			models, err := s.fetchModelsFromSource(t.Context(), storage.ModelSource{Platform: "custom:vendor-discovery", BaseURL: upstream.URL}, "")
			if err == nil || models != nil || calls > 2 {
				t.Fatal(models, calls, err)
			}
		})
	}
}

func TestModelDiscoveryDoesNotFollowRedirectOrExposeProviderBody(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateCustomDiscovery(t, s)
	for _, status := range []int{http.StatusUnauthorized, http.StatusTemporaryRedirect} {
		calls := 0
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Location", "/secret-target")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("reflected-secret"))
		}))
		_, err := s.fetchModelsFromSource(t.Context(), storage.ModelSource{Platform: "custom:vendor-discovery", BaseURL: upstream.URL}, "reflected-secret")
		upstream.Close()
		if err == nil || calls != 1 || strings.Contains(err.Error(), "reflected-secret") {
			t.Fatal(calls, err)
		}
	}
}
