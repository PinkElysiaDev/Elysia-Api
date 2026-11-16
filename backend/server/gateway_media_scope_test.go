package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

const (
	imageScopeURI = "data:image/png;base64,YQ=="
	imageScopeURL = "https://example.invalid/image.png"
)

func TestGatewayGeminiImageMIMEScope(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := defaultGatewayPackages(t, server)
	target := packages["google-generate-content"]
	provider, calls := newGatewayImageDetailProvider(t, target)
	setupGatewayImageModel(t, server, target, provider.URL)
	server.invalidateRouteCache()
	for _, sourceName := range []string{"openai-chat", "openai-responses"} {
		for _, isStream := range []bool{false, true} {
			operation := "generate"
			if isStream {
				operation = "stream"
			}
			for _, variant := range []string{"data-uri", "url", "mixed-first", "mixed-last"} {
				t.Run(sourceName+"/"+operation+"/"+variant, func(t *testing.T) {
					source := packages[sourceName]
					body := buildGatewayImageScopeRequest(sourceName, variant, isStream)
					path := "/gateway/" + source.Identity().DefinitionID + source.Operations()[operation].Path
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer gateway-test-token")
					request.Header.Set("Content-Type", "application/json")
					before := calls.Load()
					reply := httptest.NewRecorder()
					serveTestHTTP(server.engine, reply, request)
					if variant != "data-uri" {
						if reply.Code != http.StatusBadRequest || calls.Load() != before || !strings.Contains(reply.Body.String(), "/payload/mime") || !strings.Contains(reply.Body.String(), "media-with-mime") {
							t.Fatal("request outside the MIME scope reached the provider or lost its diagnostic", reply.Code, calls.Load()-before, reply.Body.String())
						}
						return
					}
					if reply.Code != http.StatusOK || calls.Load() != before+1 {
						t.Fatal("MIME-present request failed or repeated", reply.Code, calls.Load()-before, reply.Body.String())
					}
					response := collectPackageGatewayReply(t, source, reply, isStream, protocol.Scope{})
					checkPackageGatewayReply(t, response, "hi", 20, 2)
				})
			}
		}
	}
}

func buildGatewayImageScopeRequest(source, variant string, isStream bool) string {
	body := buildGatewayImageDetailRequest(source, "", isStream)
	if variant == "data-uri" {
		return body
	}
	if variant == "url" {
		return strings.Replace(body, imageScopeURI, imageScopeURL, 1)
	}
	image := `{"type":"image_url","image_url":{"url":"` + imageScopeURI + `"}}`
	if source == "openai-responses" {
		image = `{"type":"input_image","image_url":"` + imageScopeURI + `"}`
	}
	urlImage := strings.Replace(image, imageScopeURI, imageScopeURL, 1)
	images := urlImage + "," + image
	if variant == "mixed-last" {
		images = image + "," + urlImage
	}
	return strings.Replace(body, image, images, 1)
}
