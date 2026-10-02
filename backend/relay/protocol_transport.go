package relay

import (
	"context"
	"net/http"

	"github.com/elysia-api/backend/protocol"
)

// SendProtocolRequest uses the gateway's configured clients, including proxy,
// TLS and dial-time address validation, for a pinned declarative operation.
func (adapter *OpenAIAdapter) SendProtocolRequest(ctx context.Context, baseURL, credential string, operation protocol.Operation, body []byte, parameters map[string]string) (*http.Response, error) {
	request, err := protocol.BuildHTTPRequest(ctx, baseURL, credential, operation, body, parameters)
	if err != nil {
		return nil, err
	}
	var client interface {
		Do(*http.Request) (*http.Response, error)
	} = adapter.client
	if operation.Transport == protocol.SSE || operation.Transport == protocol.NDJSON {
		client = adapter.streamClient
	}
	if operation.Kind == "submit" || operation.Kind == "status" || operation.Kind == "result" || operation.Kind == "cancel" {
		adapter.client.mu.RLock()
		copy := *adapter.client.client
		adapter.client.mu.RUnlock()
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	response, err := client.Do(request)
	return response, sanitizeCustomTransportError(err)
}
