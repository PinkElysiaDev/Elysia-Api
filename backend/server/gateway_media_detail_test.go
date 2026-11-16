package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const gatewayImagePreprocessingRule = "image-default-preprocessing"

type imageDetailPreview struct {
	Output           protocol.Request
	Issues           []protocol.ConversionIssue
	Effective        protocol.CompiledConversion
	PersistentWrites bool
}

func TestGatewayAnthropicImageDetailBoundary(t *testing.T) {
	checkGatewayImageDetailTarget(t, "anthropic")
}

func TestGatewayGeminiImageDetailBoundary(t *testing.T) {
	checkGatewayImageDetailTarget(t, "google-generate-content")
}

func checkGatewayImageDetailTarget(t *testing.T, targetName string) {
	t.Helper()
	for _, mode := range []string{"compatible", "strict", "rule-disabled"} {
		t.Run(mode, func(t *testing.T) {
			checkGatewayImageDetailPolicy(t, targetName, mode)
		})
	}
}

func checkGatewayImageDetailPolicy(t *testing.T, targetName, mode string) {
	t.Helper()
	server, _ := newProtocolAdminTestServer(t)
	packages := defaultGatewayPackages(t, server)
	target := packages[targetName]
	provider, calls := newGatewayImageDetailProvider(t, target)
	setupGatewayImageModel(t, server, target, provider.URL)
	policy := buildGatewayImageDetailPolicy(t, packages["openai-chat"], target, mode)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := server.store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 1 {
		t.Fatal("expected one upstream binding", bindings, err)
	}
	bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &policy}
	bindings, err = server.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
		t.Fatal(err)
	}
	server.invalidateRouteCache()
	for _, sourceName := range []string{"openai-chat", "openai-responses"} {
		for _, isStream := range []bool{false, true} {
			operation := "generate"
			if isStream {
				operation = "stream"
			}
			for _, detail := range []string{"", "auto", "high", "low"} {
				t.Run(sourceName+"/"+operation+"/"+detail, func(t *testing.T) {
					source := packages[sourceName]
					body := buildGatewayImageDetailRequest(sourceName, detail, isStream)
					path := "/gateway/" + source.Identity().DefinitionID + source.Operations()[operation].Path
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer gateway-test-token")
					request.Header.Set("Content-Type", "application/json")
					before := calls.Load()
					reply := httptest.NewRecorder()
					serveTestHTTP(server.engine, reply, request)
					// The complete strict Responses report independently rejects nested
					// response layout. Auto is rejected earlier by its request rule.
					if mode == "strict" && sourceName == "openai-responses" && detail != "auto" {
						if reply.Code != http.StatusBadRequest || calls.Load() != before || !strings.Contains(reply.Body.String(), "/parentId") {
							t.Fatal("strict Responses layout boundary changed", reply.Code, calls.Load()-before, reply.Body.String())
						}
						return
					}
					if detail != "" && (detail != "auto" || mode != "compatible") {
						if reply.Code != http.StatusBadRequest || calls.Load() != before || !strings.Contains(reply.Body.String(), "/content/0/children/0/payload/detail") {
							t.Fatal("unsupported image detail escaped its mapping boundary", reply.Code, calls.Load()-before, reply.Body.String())
						}
						return
					}
					if reply.Code != http.StatusOK || calls.Load() != before+1 {
						t.Fatal("supported image failed or repeated", reply.Code, calls.Load()-before, reply.Body.String())
					}
					response := collectPackageGatewayReply(t, source, reply, isStream, protocol.Scope{})
					checkPackageGatewayReply(t, response, "hi", 20, 2)
				})
			}
		}
	}
}

func buildGatewayImageDetailPolicy(t *testing.T, source, target *protocol.Compiled, mode string) protocol.ConversionPolicy {
	t.Helper()
	policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "image-preprocessing-mode", Mode: "compatible", Rules: []protocol.ConversionRule{}}
	if mode == "strict" {
		policy.Mode = mode
	}
	for _, rule := range protocol.DefaultConversionPolicy(source, target).Rules {
		if rule.ID != gatewayImagePreprocessingRule {
			continue
		}
		if rule.Action != "core_controls" || !rule.Enabled || rule.Phase != protocol.ConversionRequest {
			t.Fatal("image preprocessing is not a generic enabled request default", rule)
		}
		if mode == "rule-disabled" {
			rule.Enabled = false
			policy.Rules = append(policy.Rules, rule)
		}
		return policy
	}
	t.Fatal("missing image preprocessing rule")
	return policy
}

func TestImageDetailConversionPreview(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := defaultGatewayPackages(t, server)
	for _, targetName := range []string{"anthropic", "google-generate-content"} {
		for _, sourceName := range []string{"openai-chat", "openai-responses"} {
			for _, mode := range []string{"compatible", "strict", "rule-disabled"} {
				t.Run(targetName+"/"+sourceName+"/"+mode, func(t *testing.T) {
					checkImageDetailPreview(t, server, packages[sourceName], packages[targetName], sourceName, mode)
				})
			}
		}
	}
}

func checkImageDetailPreview(t *testing.T, server *Server, source, target *protocol.Compiled, sourceName, mode string) {
	t.Helper()
	request, err := source.DecodeRequest(t.Context(), []byte(buildGatewayImageDetailRequest(sourceName, "auto", false)), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	policy := buildGatewayImageDetailPolicy(t, source, target, mode)
	body, err := sonic.ConfigStd.Marshal(map[string]any{"policy": policy, "phase": "request", "context": protocol.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "upstream-model"}, "input": request})
	if err != nil {
		t.Fatal(err)
	}
	ctx, reply := adminProtocolContext(http.MethodPost, "/api/admin/protocols/conversion-policies/preview", string(body))
	server.adminConversionPreview(contextForTest(ctx), ctx)
	flushTestResponse(ctx)
	if mode == "strict" {
		if reply.Code != http.StatusBadRequest || !strings.Contains(reply.Body.String(), gatewayImagePreprocessingRule) || !strings.Contains(reply.Body.String(), "/content/0/children/0/payload/detail") {
			t.Fatal("strict preview lost its named rejection", reply.Code, reply.Body.String())
		}
		return
	}
	if reply.Code != http.StatusOK {
		t.Fatal(reply.Code, reply.Body.String())
	}
	var result struct {
		Data imageDetailPreview
	}
	if err := sonic.ConfigStd.Unmarshal(reply.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	checkImageDetailPreviewOutput(t, target, mode, result.Data)
}

func checkImageDetailPreviewOutput(t *testing.T, target *protocol.Compiled, mode string, preview imageDetailPreview) {
	t.Helper()
	if preview.PersistentWrites {
		t.Fatal("preview performed persistent writes")
	}
	hasRule := false
	for _, rule := range preview.Effective.Policy.Rules {
		if rule.ID == gatewayImagePreprocessingRule {
			hasRule = true
			if rule.Action != "core_controls" || rule.Enabled != (mode == "compatible") {
				t.Fatal("preview did not expose the effective generic rule", rule)
			}
		}
	}
	if !hasRule {
		t.Fatal("preview omitted its image preprocessing rule")
	}
	media, err := preview.Output.Content[0].Children[0].Payload.ReadObject()
	if err != nil || !media["mime"].IsString("image/png") || !media["data"].IsString("YQ==") {
		t.Fatal("preview changed image MIME or bytes", media, err)
	}
	if mode == "rule-disabled" {
		if !media["detail"].IsString("auto") || len(preview.Issues) != 0 {
			t.Fatal("disabled preprocessing changed image intent", preview.Issues, media)
		}
		_, err := target.EncodeRequest(t.Context(), &preview.Output, protocol.EvaluationContext{})
		var failure *protocol.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != protocol.ConversionRejected || failure.Issues[0].Path != "/content/0/children/0/payload/detail" {
			t.Fatal("disabled preview did not reach the target detail guard", err)
		}
		return
	}
	if len(media) != 2 || len(preview.Issues) != 1 {
		t.Fatal("compatible preview did not expose exactly one loss", media, preview.Issues)
	}
	issue := preview.Issues[0]
	if issue.RuleID != gatewayImagePreprocessingRule || issue.Code != protocol.ConversionDegraded || issue.Severity != protocol.SeverityWarning || issue.Fidelity != "lossy_compatible" || issue.Path != "/content/0/children/0/payload/detail" || issue.PolicyHash == "" || issue.PolicyRevision == "" {
		t.Fatal("preview image preprocessing diagnostic changed", issue)
	}
	if _, err := target.EncodeRequest(t.Context(), &preview.Output, protocol.EvaluationContext{}); err != nil {
		t.Fatal("preview output failed target encoding", err)
	}
}

func setupGatewayImageModel(t *testing.T, server *Server, target *protocol.Compiled, providerURL string) {
	t.Helper()
	setupGatewayModelWithCapabilities(t, server, target, providerURL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.ImagesCapability: true, protocol.UsageCapability: true})
	hasVision := true
	if _, err := server.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{VisionCapable: &hasVision}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, VisionCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
}

func newGatewayImageDetailProvider(t *testing.T, target *protocol.Compiled) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	jsonReply := buildPackageGatewayReply(t, target, false)
	streamReply := buildPackageGatewayReply(t, target, true)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		semantic, err := target.DecodeRequest(request.Context(), body, protocol.EvaluationContext{})
		if err != nil {
			t.Error("invalid image request reached upstream", err)
			return
		}
		if len(semantic.Content) != 1 || len(semantic.Content[0].Children) != 1 {
			t.Error("image owner changed", string(body))
			return
		}
		image := semantic.Content[0].Children[0]
		media, err := image.Payload.ReadObject()
		if err != nil || image.Kind != protocol.ImageNode || !media["mime"].IsString("image/png") || !media["data"].IsString("YQ==") || len(media) != 2 {
			t.Error("image MIME, bytes or detail changed", image, err)
		}
		isStream := semantic.Parameters["stream"].JSONString() == "true" || request.URL.Query().Get("alt") == "sse"
		if isStream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write(streamReply)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonReply)
	}))
	t.Cleanup(provider.Close)
	return provider, &calls
}

func buildGatewayImageDetailRequest(source, detail string, isStream bool) string {
	field := ""
	if detail != "" {
		field = `,"detail":"` + detail + `"`
	}
	if source == "openai-chat" {
		usage := ""
		if isStream {
			usage = `,"stream_options":{"include_usage":true}`
		}
		return fmt.Sprintf(`{"model":"group","max_completion_tokens":64,"stream":%t%s,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YQ=="%s}}]}]}`, isStream, usage, field)
	}
	return fmt.Sprintf(`{"model":"group","max_output_tokens":64,"store":false,"stream":%t,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,YQ=="%s}]}]}`, isStream, field)
}
